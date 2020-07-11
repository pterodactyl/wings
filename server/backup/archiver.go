package backup

import (
	"archive/tar"
	"context"
	"github.com/apex/log"
	gzip "github.com/klauspost/pgzip"
	"github.com/remeh/sizedwaitgroup"
	"golang.org/x/sync/errgroup"
	"io"
	"os"
	"strings"
	"sync"
)

type Archive struct {
	sync.Mutex

	TrimPrefix string
	Files      *IncludedFiles
}

// Creates an archive at dest with all of the files definied in the included files struct.
func (a *Archive) Create(dest string, ctx context.Context) (os.FileInfo, error) {
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gzw := gzip.NewWriter(f)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	wg := sizedwaitgroup.New(10)
	g, ctx := errgroup.WithContext(ctx)
	// Iterate over all of the files to be included and put them into the archive. This is
	// done as a concurrent goroutine to speed things along. If an error is encountered at
	// any step, the entire process is aborted.
	for p, s := range a.Files.All() {
		if (*s).IsDir() {
			continue
		}

		pa := p
		st := s

		g.Go(func() error {
			wg.Add()
			defer wg.Done()

			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return a.addToArchive(pa, st, tw)
			}
		})
	}

	// Block until the entire routine is completed.
	if err := g.Wait(); err != nil {
		f.Close()

		// Attempt to remove the archive if there is an error, report that error to
		// the logger if it fails.
		if rerr := os.Remove(dest); rerr != nil && !os.IsNotExist(rerr) {
			log.WithField("location", dest).Warn("failed to delete corrupted backup archive")
		}

		return nil, err
	}

	st, _ := f.Stat()

	return st, nil
}

// Adds a single file to the existing tar archive writer.
func (a *Archive) addToArchive(p string, s *os.FileInfo, w *tar.Writer) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()

	st := *s
	header := &tar.Header{
		// Trim the long server path from the name of the file so that the resulting
		// archive is exactly how the user would see it in the panel file manager.
		Name:    strings.TrimPrefix(p, a.TrimPrefix),
		Size:    st.Size(),
		Mode:    int64(st.Mode()),
		ModTime: st.ModTime(),
	}

	// These actions must occur sequentially, even if this function is called multiple
	// in parallel. You'll get some nasty panic's otherwise.
	a.Lock()
	defer a.Unlock()

	if err = w.WriteHeader(header); err != nil {
		return err
	}

	if _, err := io.Copy(w, f); err != nil {
		return err
	}

	return nil
}
