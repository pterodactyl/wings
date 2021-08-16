package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/google/uuid"

	"github.com/pterodactyl/wings/server"
)

var client = &http.Client{
	Timeout: time.Hour * 12,
	// Disallow any redirect on an HTTP call. This is a security requirement: do not modify
	// this logic without first ensuring that the new target location IS NOT within the current
	// instance's local network.
	//
	// This specific error response just causes the client to not follow the redirect and
	// returns the actual redirect response to the caller. Not perfect, but simple and most
	// people won't be using URLs that redirect anyways hopefully?
	//
	// We'll re-evaluate this down the road if needed.
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

var instance = &Downloader{
	// Tracks all the active downloads.
	downloadCache: make(map[string]*Download),
	// Tracks all the downloads active for a given server instance. This is
	// primarily used to make things quicker and keep the code a little more
	// legible throughout here.
	serverCache: make(map[string][]string),
}

// Internal IP ranges that should be blocked if the resource requested resolves within.
var internalRanges = []*net.IPNet{
	mustParseCIDR("127.0.0.1/8"),
	mustParseCIDR("10.0.0.0/8"),
	mustParseCIDR("172.16.0.0/12"),
	mustParseCIDR("192.168.0.0/16"),
	mustParseCIDR("169.254.0.0/16"),
	mustParseCIDR("::1/128"),
	mustParseCIDR("fe80::/10"),
	mustParseCIDR("fc00::/7"),
}

const (
	ErrInternalResolution = errors.Sentinel("downloader: destination resolves to internal network location")
	ErrInvalidIPAddress   = errors.Sentinel("downloader: invalid IP address")
	ErrDownloadFailed     = errors.Sentinel("downloader: download request failed")
)

type Counter struct {
	total   int
	onWrite func(total int)
}

func (c *Counter) Write(p []byte) (int, error) {
	n := len(p)
	c.total += n
	c.onWrite(c.total)
	return n, nil
}

type DownloadRequest struct {
	Directory string
	URL       *url.URL
}

type Download struct {
	Identifier string
	mu         sync.RWMutex
	req        DownloadRequest
	server     *server.Server
	progress   float64
	cancelFunc *context.CancelFunc
}

// New starts a new tracked download which allows for cancellation later on by calling
// the Downloader.Cancel function.
func New(s *server.Server, r DownloadRequest) *Download {
	dl := Download{
		Identifier: uuid.Must(uuid.NewRandom()).String(),
		req:        r,
		server:     s,
	}
	instance.track(&dl)
	return &dl
}

// ByServer returns all the tracked downloads for a given server instance.
func ByServer(sid string) []*Download {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	var downloads []*Download
	if v, ok := instance.serverCache[sid]; ok {
		for _, id := range v {
			if dl, ok := instance.downloadCache[id]; ok {
				downloads = append(downloads, dl)
			}
		}
	}
	return downloads
}

// ByID returns a single Download matching a given identifier. If no download is found
// the second argument in the response will be false.
func ByID(dlid string) *Download {
	return instance.find(dlid)
}

//goland:noinspection GoVetCopyLock
func (dl Download) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Identifier string
		Progress   float64
	}{
		Identifier: dl.Identifier,
		Progress:   dl.Progress(),
	})
}

// Execute executes a given download for the server and begins writing the file to the disk. Once
// completed the download will be removed from the cache.
func (dl *Download) Execute() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour*12)
	dl.cancelFunc = &cancel
	defer dl.Cancel()

	// Always ensure that we're checking the destination for the download to avoid a malicious
	// user from accessing internal network resources.
	if err := dl.isExternalNetwork(ctx); err != nil {
		return err
	}

	// At this point we have verified the destination is not within the local network, so we can
	// now make a request to that URL and pull down the file, saving it to the server's data
	// directory.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dl.req.URL.String(), nil)
	if err != nil {
		return errors.WrapIf(err, "downloader: failed to create request")
	}

	req.Header.Set("User-Agent", "Pterodactyl Panel (https://pterodactyl.io)")
	res, err := client.Do(req)
	if err != nil {
		return ErrDownloadFailed
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New("downloader: got bad response status from endpoint: " + res.Status)
	}

	// If there is a Content-Length header on this request go ahead and check that we can
	// even write the whole file before beginning this process. If there is no header present
	// we'll just have to give it a spin and see how it goes.
	if res.ContentLength > 0 {
		if err := dl.server.Filesystem().HasSpaceFor(res.ContentLength); err != nil {
			return errors.WrapIf(err, "downloader: failed to write file: not enough space")
		}
	}

	fnameparts := strings.Split(dl.req.URL.Path, "/")
	p := filepath.Join(dl.req.Directory, fnameparts[len(fnameparts)-1])
	dl.server.Log().WithField("path", p).Debug("writing remote file to disk")

	r := io.TeeReader(res.Body, dl.counter(res.ContentLength))
	if err := dl.server.Filesystem().Writefile(p, r); err != nil {
		return errors.WrapIf(err, "downloader: failed to write file to server directory")
	}
	return nil
}

// Cancel cancels a running download and frees up the associated resources. If a file is being
// written a partial file will remain present on the disk.
func (dl *Download) Cancel() {
	if dl.cancelFunc != nil {
		(*dl.cancelFunc)()
	}
	instance.remove(dl.Identifier)
}

// BelongsTo checks if the given download belongs to the provided server.
func (dl *Download) BelongsTo(s *server.Server) bool {
	return dl.server.ID() == s.ID()
}

// Progress returns the current progress of the download as a float value between 0 and 1 where
// 1 indicates that the download is completed.
func (dl *Download) Progress() float64 {
	dl.mu.RLock()
	defer dl.mu.RUnlock()
	return dl.progress
}

// Handles a write event by updating the progress completed percentage and firing off
// events to the server websocket as needed.
func (dl *Download) counter(contentLength int64) *Counter {
	onWrite := func(t int) {
		dl.mu.Lock()
		defer dl.mu.Unlock()
		dl.progress = float64(t) / float64(contentLength)
	}
	return &Counter{
		onWrite: onWrite,
	}
}

// Verifies that a given download resolves to a location not within the current local
// network for the machine. If the final destination of a resource is within the local
// network an ErrInternalResolution error is returned.
func (dl *Download) isExternalNetwork(ctx context.Context) error {
	dialer := &net.Dialer{
		LocalAddr: nil,
	}

	host := dl.req.URL.Host

	// This cluster-fuck of math and integer shit converts an integer IP into a proper IPv4.
	// For example: 16843009 would become 1.1.1.1
	//if i, err := strconv.ParseInt(host, 10, 64); err == nil {
	//	host = strconv.FormatInt((i>>24)&0xFF, 10) + "." + strconv.FormatInt((i>>16)&0xFF, 10) + "." + strconv.FormatInt((i>>8)&0xFF, 10) + "." + strconv.FormatInt(i&0xFF, 10)
	//}

	if _, _, err := net.SplitHostPort(host); err != nil {
		if !strings.Contains(err.Error(), "missing port in address") {
			return errors.WithStack(err)
		}
		switch dl.req.URL.Scheme {
		case "http":
			host += ":80"
		case "https":
			host += ":443"
		}
	}

	c, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return errors.WithStack(err)
	}
	_ = c.Close()

	ipStr, _, err := net.SplitHostPort(c.RemoteAddr().String())
	if err != nil {
		return errors.WithStack(err)
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return errors.WithStack(ErrInvalidIPAddress)
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return errors.WithStack(ErrInternalResolution)
	}
	for _, block := range internalRanges {
		if block.Contains(ip) {
			return errors.WithStack(ErrInternalResolution)
		}
	}
	return nil
}

// Downloader represents a global downloader that keeps track of all currently processing downloads
// for the machine.
type Downloader struct {
	mu            sync.RWMutex
	downloadCache map[string]*Download
	serverCache   map[string][]string
}

// track tracks a download in the internal cache for this instance.
func (d *Downloader) track(dl *Download) {
	d.mu.Lock()
	defer d.mu.Unlock()
	sid := dl.server.ID()
	if _, ok := d.downloadCache[dl.Identifier]; !ok {
		d.downloadCache[dl.Identifier] = dl
		if _, ok := d.serverCache[sid]; !ok {
			d.serverCache[sid] = []string{}
		}
		d.serverCache[sid] = append(d.serverCache[sid], dl.Identifier)
	}
}

// find finds a given download entry using the provided ID and returns it.
func (d *Downloader) find(dlid string) *Download {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if entry, ok := d.downloadCache[dlid]; ok {
		return entry
	}
	return nil
}

// remove removes the given download reference from the cache storing them. This also updates
// the slice of active downloads for a given server to not include this download.
func (d *Downloader) remove(dlID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.downloadCache[dlID]; !ok {
		return
	}
	sID := d.downloadCache[dlID].server.ID()
	delete(d.downloadCache, dlID)
	if tracked, ok := d.serverCache[sID]; ok {
		var out []string
		for _, k := range tracked {
			if k != dlID {
				out = append(out, k)
			}
		}
		d.serverCache[sID] = out
	}
}

func mustParseCIDR(ip string) *net.IPNet {
	_, block, err := net.ParseCIDR(ip)
	if err != nil {
		panic(fmt.Errorf("downloader: failed to parse CIDR: %s", err))
	}
	return block
}
