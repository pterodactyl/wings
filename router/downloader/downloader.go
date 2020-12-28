package downloader

import (
	"context"
	"emperror.dev/errors"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/pterodactyl/wings/server"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
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

type Downloader struct {
	mu            sync.RWMutex
	downloadCache map[string]*Download
	serverCache   map[string][]string
}

type DownloadRequest struct {
	URL       *url.URL
	Directory string
}

type Download struct {
	Identifier string
	mu         sync.RWMutex
	req        DownloadRequest
	server     *server.Server
	progress   float64
	cancelFunc *context.CancelFunc
}

var client = &http.Client{Timeout: time.Hour * 12}
var instance = &Downloader{
	// Tracks all of the active downloads.
	downloadCache: make(map[string]*Download),
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
	instance.track(&dl)
	return &dl
}

// Returns all of the tracked downloads for a given server instance.
func ByServer(sid string) []*Download {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	var downloads []*Download
	if v, ok := instance.serverCache[sid]; ok {
		for _, id := range v {
			if dl, dlok := instance.downloadCache[id]; dlok {
				downloads = append(downloads, dl)
			}
		}
	}
	return downloads
}

// Returns a single Download matching a given identifier. If no download is found
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

// Executes a given download for the server and begins writing the file to the disk. Once
// completed the download will be removed from the cache.
func (dl *Download) Execute() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour*12)
	dl.cancelFunc = &cancel
	defer dl.Cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dl.req.URL.String(), nil)
	if err != nil {
		return errors.WrapIf(err, "downloader: failed to create request")
	}

	req.Header.Set("User-Agent", "Pterodactyl Panel (https://pterodactyl.io)")
	res, err := client.Do(req) // lgtm [go/request-forgery]
	if err != nil {
		return errors.New("downloader: failed opening request to download file")
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

// Cancels a running download and frees up the associated resources. If a file is being
// written a partial file will remain present on the disk.
func (dl *Download) Cancel() {
	if dl.cancelFunc != nil {
		(*dl.cancelFunc)()
	}
	instance.remove(dl.Identifier)
}

// Checks if the given download belongs to the provided server.
func (dl *Download) BelongsTo(s *server.Server) bool {
	return dl.server.Id() == s.Id()
}

// Returns the current progress of the download as a float value between 0 and 1 where
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

// Tracks a download in the internal cache for this instance.
func (d *Downloader) track(dl *Download) {
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
func (d *Downloader) find(dlid string) *Download {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if entry, ok := d.downloadCache[dlid]; ok {
		return entry
	}
	return nil
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
