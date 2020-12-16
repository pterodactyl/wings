package backup

import (
	"archive/tar"
	"context"
	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/juju/ratelimit"
	gzip "github.com/klauspost/pgzip"
	"github.com/pterodactyl/wings/config"
	"github.com/remeh/sizedwaitgroup"
	"golang.org/x/sync/errgroup"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
)

type Archive struct {
	sync.Mutex

	TrimPrefix string
	Files      *IncludedFiles
}

// Creates an archive at dst with all of the files defined in the included files struct.
func (a *Archive) Create(dst string, ctx context.Context) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	// Select a writer based off of the WriteLimit configuration option.
	var writer io.Writer
	if writeLimit := config.Get().System.Backups.WriteLimit; writeLimit < 1 {
		// If there is no write limit, use the file as the writer.
		writer = f
	} else {
		// Token bucket with a capacity of "writeLimit" MiB, adding "writeLimit" MiB/s
		bucket := ratelimit.NewBucketWithRate(float64(writeLimit)*1024*1024, int64(writeLimit)*1024*1024)

		// Wrap the file writer with the token bucket limiter.
		writer = ratelimit.Writer(f, bucket)
	}

	maxCpu := runtime.NumCPU() / 2
	if maxCpu > 4 {
		maxCpu = 4
	}

	gzw, err := gzip.NewWriterLevel(writer, gzip.BestSpeed)
	if err != nil {
		return errors.WithMessage(err, "failed to create gzip writer")
	}
	if err := gzw.SetConcurrency(1<<20, maxCpu); err != nil {
		return errors.WithMessage(err, "failed to set gzip concurrency")
	}

	defer gzw.Flush()
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Flush()
	defer tw.Close()

	wg := sizedwaitgroup.New(10)
	g, ctx := errgroup.WithContext(ctx)
	// Iterate over all of the files to be included and put them into the archive. This is
	// done as a concurrent goroutine to speed things along. If an error is encountered at
	// any step, the entire process is aborted.
	for _, p := range a.Files.All() {
		p := p
		g.Go(func() error {
			wg.Add()
			defer wg.Done()

			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return a.addToArchive(p, tw)
			}
		})
	}

	// Block until the entire routine is completed.
	if err := g.Wait(); err != nil {
		f.Close()

		// Attempt to remove the archive if there is an error, report that error to
		// the logger if it fails.
		if rerr := os.Remove(dst); rerr != nil && !os.IsNotExist(rerr) {
			log.WithField("location", dst).Warn("failed to delete corrupted backup archive")
		}

		return err
	}

	return nil
}

// Adds a single file to the existing tar archive writer.
func (a *Archive) addToArchive(p string, w *tar.Writer) error {
	f, err := os.Open(p)
	if err != nil {
		// If you try to backup something that no longer exists (got deleted somewhere during the process
		// but not by this process), just skip over it and don't kill the entire backup.
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}
	defer f.Close()

	s, err := f.Stat()
	if err != nil {
		// Same as above, don't kill the process just because the file no longer exists.
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	name := strings.TrimPrefix(p, a.TrimPrefix)
	header, err := tar.FileInfoHeader(s, name)
	if err != nil {
		return errors.WithMessage(err, "failed to get tar#FileInfoHeader for "+name)
	}
	header.Name = name

	// These actions must occur sequentially, even if this function is called multiple
	// in parallel. You'll get some nasty panic's otherwise.
	a.Lock()
	defer a.Unlock()

	if err := w.WriteHeader(header); err != nil {
		return err
	}

	buf := make([]byte, 4*1024)
	if _, err := io.CopyBuffer(w, io.LimitReader(f, header.Size), buf); err != nil {
		return errors.WithMessage(err, "failed to copy "+header.Name+" to archive")
	}

	return nil
}
