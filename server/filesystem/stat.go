package filesystem

import (
	"encoding/json"
	"github.com/gabriel-vasile/mimetype"
	"os"
	"strconv"
	"time"
)

type Stat struct {
	Info     os.FileInfo
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
		Name:     s.Info.Name(),
		Created:  s.CTime().Format(time.RFC3339),
		Modified: s.Info.ModTime().Format(time.RFC3339),
		Mode:     s.Info.Mode().String(),
		// Using `&os.ModePerm` on the file's mode will cause the mode to only have the permission values, and nothing else.
		ModeBits:  strconv.FormatUint(uint64(s.Info.Mode()&os.ModePerm), 8),
		Size:      s.Info.Size(),
		Directory: s.Info.IsDir(),
		File:      !s.Info.IsDir(),
		Symlink:   s.Info.Mode().Perm()&os.ModeSymlink != 0,
		Mime:      s.Mimetype,
	})
}

// Stats a file or folder and returns the base stat object from go along with the
// MIME data that can be used for editing files.
func (fs *Filesystem) Stat(p string) (*Stat, error) {
	cleaned, err := fs.SafePath(p)
	if err != nil {
		return nil, err
	}

	return fs.unsafeStat(cleaned)
}

func (fs *Filesystem) unsafeStat(p string) (*Stat, error) {
	s, err := os.Stat(p)
	if err != nil {
		return nil, err
	}

	var m *mimetype.MIME
	if !s.IsDir() {
		m, err = mimetype.DetectFile(p)
		if err != nil {
			return nil, err
		}
	}

	st := &Stat{
		Info:     s,
		Mimetype: "inode/directory",
	}

	if m != nil {
		st.Mimetype = m.String()
	}

	return st, nil
}
