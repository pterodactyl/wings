package filesystem

import (
	"encoding/json"
	"io"
	"strconv"
	"time"

	"github.com/gabriel-vasile/mimetype"

	"github.com/pterodactyl/wings/internal/ufs"
)

type Stat struct {
	ufs.FileInfo
	Mimetype string
}

func (s *Stat) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name      string `json:"name"`
		Created   string `json:"created"`
		Modified  string `json:"modified"`
		Mode      string `json:"mode"`
		ModeBits  string `json:"mode_bits"`
		Size      int64  `json:"size"`
		Directory bool   `json:"directory"`
		File      bool   `json:"file"`
		Symlink   bool   `json:"symlink"`
		Mime      string `json:"mime"`
	}{
		Name:     s.Name(),
		Created:  s.CTime().Format(time.RFC3339),
		Modified: s.ModTime().Format(time.RFC3339),
		Mode:     s.Mode().String(),
		// Using `&ModePerm` on the file's mode will cause the mode to only have the permission values, and nothing else.
		ModeBits:  strconv.FormatUint(uint64(s.Mode()&ufs.ModePerm), 8),
		Size:      s.Size(),
		Directory: s.IsDir(),
		File:      !s.IsDir(),
		Symlink:   s.Mode().Perm()&ufs.ModeSymlink != 0,
		Mime:      s.Mimetype,
	})
}

func statFromFile(f ufs.File) (Stat, error) {
	s, err := f.Stat()
	if err != nil {
		return Stat{}, err
	}
	var m *mimetype.MIME
	if !s.IsDir() {
		m, err = mimetype.DetectReader(f)
		if err != nil {
			return Stat{}, err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return Stat{}, err
		}
	}
	st := Stat{
		FileInfo: s,
		Mimetype: "inode/directory",
	}
	if m != nil {
		st.Mimetype = m.String()
	}
	return st, nil
}

// Stat stats a file or folder and returns the base stat object from go along
// with the MIME data that can be used for editing files.
func (fs *Filesystem) Stat(p string) (Stat, error) {
	f, err := fs.unixFS.Open(p)
	if err != nil {
		return Stat{}, err
	}
	defer f.Close()
	st, err := statFromFile(f)
	if err != nil {
		return Stat{}, err
	}
	return st, nil
}
