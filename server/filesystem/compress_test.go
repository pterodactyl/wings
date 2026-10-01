package filesystem

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/franela/goblin"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// Given an archive named test.{ext}, with the following file structure:
//
//	test/
//	|──inside/
//	|────finside.txt
//	|──outside.txt
//
// this test will ensure that it's being decompressed as expected
func TestFilesystem_DecompressFile(t *testing.T) {
	g := Goblin(t)
	fs, rfs := NewFs()

	g.Describe("Decompress", func() {
		for _, ext := range []string{"zip", "rar", "tar", "tar.gz"} {
			g.It("can decompress a "+ext, func() {
				// copy the file to the new FS
				c, err := os.ReadFile("./testdata/test." + ext)
				g.Assert(err).IsNil()
				err = rfs.CreateServerFile("./test."+ext, c)
				g.Assert(err).IsNil()

				// decompress
				err = fs.DecompressFile(context.Background(), "/", "test."+ext)
				g.Assert(err).IsNil()

				// make sure everything is where it is supposed to be
				_, err = rfs.StatServerFile("test/outside.txt")
				g.Assert(err).IsNil()

				st, err := rfs.StatServerFile("test/inside")
				g.Assert(err).IsNil()
				g.Assert(st.IsDir()).IsTrue()

				_, err = rfs.StatServerFile("test/inside/finside.txt")
				g.Assert(err).IsNil()
				g.Assert(st.IsDir()).IsTrue()
			})
		}

		g.AfterEach(func() {
			_ = fs.TruncateRootDirectory()
		})
	})
}

// Empty directories have no file to create them implicitly, so extraction must
// create them explicitly or they are dropped.
func TestFilesystem_DecompressFileEmptyDirectory(t *testing.T) {
	g := Goblin(t)
	fs, rfs := NewFs()

	g.Describe("Decompress", func() {
		archives := []struct {
			name  string
			build func() ([]byte, error)
		}{
			{"empty.zip", zipWithEmptyDir},
			{"empty.tar.gz", tarGzWithEmptyDir},
		}

		for _, a := range archives {
			g.It("preserves an empty directory in a "+a.name, func() {
				content, err := a.build()
				g.Assert(err).IsNil()
				err = rfs.CreateServerFile("./"+a.name, content)
				g.Assert(err).IsNil()

				err = fs.DecompressFile(context.Background(), "/", a.name)
				g.Assert(err).IsNil()

				// The empty directory must exist, and the sibling file must still extract.
				st, err := rfs.StatServerFile("empty")
				g.Assert(err).IsNil()
				g.Assert(st.IsDir()).IsTrue()

				_, err = rfs.StatServerFile("outside.txt")
				g.Assert(err).IsNil()
			})
		}

		g.AfterEach(func() {
			_ = fs.TruncateRootDirectory()
		})
	})
}

// zipWithEmptyDir builds a zip holding one file and an empty directory ("empty/").
func zipWithEmptyDir() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	dh := &zip.FileHeader{Name: "empty/"}
	dh.SetMode(os.ModeDir | 0o755)
	if _, err := zw.CreateHeader(dh); err != nil {
		return nil, err
	}

	w, err := zw.Create("outside.txt")
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// A GBK path like the CustomNPCs dialog archive must survive the disk check
// and land on disk as UTF-8. UTF-8 stored without the ZIP flag stays UTF-8.
func TestFilesystem_DecompressLegacyZipNames(t *testing.T) {
	fs, rfs := NewFs()
	t.Cleanup(func() { _ = fs.TruncateRootDirectory() })
	fs.SetDiskLimit(10 * 1024 * 1024)

	t.Run("gbk directory", func(t *testing.T) {
		dir := mustEncode(t, simplifiedchinese.GB18030, "world/customnpcs/dialogs/河北每日/")
		file := mustEncode(t, simplifiedchinese.GB18030, "world/customnpcs/dialogs/河北每日/对话.txt")
		data, err := buildRawZip([]rawZipEntry{
			{name: dir, dir: true},
			{name: file, body: []byte("dialog-body")},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := rfs.CreateServerFile("legacy.zip", data); err != nil {
			t.Fatal(err)
		}
		if err := fs.SpaceAvailableForDecompression(context.Background(), "/", "legacy.zip"); err != nil {
			t.Fatalf("space check: %v", err)
		}
		if err := fs.DecompressFile(context.Background(), "/", "legacy.zip"); err != nil {
			t.Fatalf("decompress: %v", err)
		}
		st, err := rfs.StatServerFile("world/customnpcs/dialogs/河北每日")
		if err != nil {
			t.Fatalf("stat dir: %v", err)
		}
		if !st.IsDir() {
			t.Fatal("河北每日 is not a directory")
		}
		got, err := os.ReadFile(filepath.Join(rfs.root, "server", "world/customnpcs/dialogs/河北每日/对话.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "dialog-body" {
			t.Fatalf("body = %q", got)
		}
		_ = fs.TruncateRootDirectory()
	})

	t.Run("utf-8 without flag", func(t *testing.T) {
		data, err := buildRawZip([]rawZipEntry{
			{name: "中文.txt", body: []byte("plain"), utf8: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		data = clearZipUTF8Flag(t, data)
		if err := rfs.CreateServerFile("utf8.zip", data); err != nil {
			t.Fatal(err)
		}
		if err := fs.DecompressFile(context.Background(), "/", "utf8.zip"); err != nil {
			t.Fatalf("decompress: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(rfs.root, "server", "中文.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "plain" {
			t.Fatalf("body = %q", got)
		}
	})
}

// tarGzWithEmptyDir builds a tar.gz holding one file and an empty directory ("empty/").
func tarGzWithEmptyDir() ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	if err := tw.WriteHeader(&tar.Header{Name: "empty/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		return nil, err
	}

	content := []byte("hello")
	if err := tw.WriteHeader(&tar.Header{Name: "outside.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(content); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
