package downloader

import (
	"context"
	"emperror.dev/errors"
	"github.com/google/uuid"
	"github.com/pterodactyl/wings/server"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Downloader struct {
	mu            sync.RWMutex
	downloadCache map[string]Download
	serverCache   map[string][]string
}

type DownloadRequest struct {
	URL       *url.URL
	Directory string
}

type Download struct {
	Identifier string
	req        DownloadRequest
	server     *server.Server
	cancelFunc *context.CancelFunc
}

var client = &http.Client{Timeout: time.Hour * 12}
var instance = &Downloader{
	// Tracks all of the active downloads.
	downloadCache: make(map[string]Download),
	// Tracks all of the downloads active for a given server instance. This is
	// primarily used to make things quicker and keep the code a little more
	// legible throughout here.
	serverCache: make(map[string][]string),
}

// Starts a new tracked download which allows for cancelation later on by calling
// the Downloader.Cancel function.
func New(s *server.Server, r DownloadRequest) *Download {
	dl := Download{
		Identifier: uuid.Must(uuid.NewRandom()).String(),
		req:        r,
		server:     s,
	}
	instance.track(dl)
	return &dl
}

// Executes a given download for the server and begins writing the file to the disk. Once
// completed the download will be removed from the cache.
func (dl *Download) Execute() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour*12)
	dl.cancelFunc = &cancel
	defer dl.Cancel()

	fnameparts := strings.Split(dl.req.URL.Path, "/")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, dl.req.URL.String(), nil)
	res, err := client.Do(req)
	if err != nil {
		return errors.New("downloader: failed opening request to download file")
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 || res.StatusCode < 200 {
		return errors.New("downloader: got bad response status from endpoint: " + res.Status)
	}
	p := filepath.Join(dl.req.Directory, fnameparts[len(fnameparts)-1])
	dl.server.Log().WithField("path", p).Debug("writing remote file to disk")
	if err := dl.server.Filesystem().Writefile(p, res.Body); err != nil {
		return errors.WrapIf(err, "downloader: failed to write file to server directory")
	}
	return nil
}

// Cancels a running download and frees up the associated resources. If a file is being
// written a partial file will remain present on the disk.
func (dl *Download) Cancel() {
	if dl.cancelFunc != nil {
		(*dl.cancelFunc)()
	}
	instance.remove(dl.Identifier)
}

// Tracks a download in the internal cache for this instance.
func (d *Downloader) track(dl Download) {
	d.mu.Lock()
	defer d.mu.Unlock()
	sid := dl.server.Id()
	if _, ok := d.downloadCache[dl.Identifier]; !ok {
		d.downloadCache[dl.Identifier] = dl
		if _, ok := d.serverCache[sid]; !ok {
			d.serverCache[sid] = []string{}
		}
		d.serverCache[sid] = append(d.serverCache[sid], dl.Identifier)
	}
}

// Finds a given download entry using the provided ID and returns it.
func (d *Downloader) find(dlid string) (Download, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if entry, ok := d.downloadCache[dlid]; ok {
		return entry, true
	}
	return Download{}, false
}

// Remove the given download reference from the cache storing them. This also updates
// the slice of active downloads for a given server to not include this download.
func (d *Downloader) remove(dlid string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.downloadCache[dlid]; !ok {
		return
	}
	sid := d.downloadCache[dlid].server.Id()
	delete(d.downloadCache, dlid)
	if tracked, ok := d.serverCache[sid]; ok {
		var out []string
		for _, k := range tracked {
			if k != dlid {
				out = append(out, k)
			}
		}
		d.serverCache[sid] = out
	}
}
