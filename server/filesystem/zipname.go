package filesystem

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/klauspost/compress/zip"
	"github.com/mholt/archives"
	"golang.org/x/text/encoding"
)

// zipUnicodePathExtra is the Info-ZIP Unicode Path extra field (0x7075).
const zipUnicodePathExtra uint16 = 0x7075

// archiveNames is the legacy filename encoding chosen for one zip archive.
// Non-zip entries are decoded one name at a time.
type archiveNames struct {
	legacy encoding.Encoding
}

// cleanArchivePath turns a ZIP name into a relative slash-separated path.
// Windows separators and a leading drive letter are removed so filepath.Join
// keeps the destination directory. ".." is left for the filesystem sandbox.
// A trailing slash is preserved because zip uses it to mark directories.
func cleanArchivePath(name string) string {
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.ReplaceAll(name, "\x00", "")
	dir := strings.HasSuffix(name, "/")
	name = strings.TrimLeft(name, "/")
	if len(name) >= 2 && name[1] == ':' && isASCIILetter(name[0]) {
		name = strings.TrimLeft(name[2:], "/")
	}
	name = path.Clean(name)
	if name == "." {
		if dir {
			return ""
		}
		return "."
	}
	if dir && !strings.HasSuffix(name, "/") {
		name += "/"
	}
	return name
}

func isASCIILetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// infoZipUnicodeName returns the UTF-8 path from extra field 0x7075 when its
// CRC-32 matches the raw header name.
func infoZipUnicodeName(extra []byte, raw string) (string, bool) {
	b := extra
	for len(b) >= 4 {
		id := binary.LittleEndian.Uint16(b[0:2])
		size := int(binary.LittleEndian.Uint16(b[2:4]))
		b = b[4:]
		if size > len(b) {
			return "", false
		}
		field := b[:size]
		b = b[size:]
		if id != zipUnicodePathExtra || len(field) < 5 || field[0] != 1 {
			continue
		}
		crc := binary.LittleEndian.Uint32(field[1:5])
		if crc != crc32.ChecksumIEEE([]byte(raw)) {
			continue
		}
		name := string(field[5:])
		if name == "" || !utf8.ValidString(name) || strings.ContainsRune(name, '\uFFFD') {
			continue
		}
		return name, true
	}
	return "", false
}

// decodeZipName resolves one archive entry name.
// Unicode Path extra wins, then a name that is already valid UTF-8 (even when
// the ZIP UTF-8 flag is missing). Only the remaining bytes use legacy.
func decodeZipName(raw string, extra []byte, legacy encoding.Encoding) string {
	if name, ok := infoZipUnicodeName(extra, raw); ok {
		return cleanArchivePath(name)
	}
	if utf8.ValidString(raw) {
		return cleanArchivePath(raw)
	}
	if legacy != nil {
		if text, ok := roundTrip(legacy, raw); ok {
			return cleanArchivePath(text)
		}
	}
	return cleanArchivePath(raw)
}

// normalizeZipNames rewrites names on a klauspost zip reader before its fs.FS
// index is built. The index is lazy, and fs.ValidPath rejects non-UTF-8 names,
// which is what made decompression of Chinese paths fail the disk check.
func normalizeZipNames(files []*zip.File) {
	raw := make([]string, 0, len(files))
	for _, f := range files {
		if f == nil {
			continue
		}
		if _, ok := infoZipUnicodeName(f.Extra, f.Name); ok || utf8.ValidString(f.Name) {
			continue
		}
		raw = append(raw, f.Name)
	}
	legacy := detectLegacyEncoding(raw)
	for _, f := range files {
		if f == nil {
			continue
		}
		name := decodeZipName(f.Name, f.Extra, legacy)
		if name == "" || name == "." {
			continue
		}
		f.Name = name
	}
}

// zipArchiveNames detects one legacy encoding for a zip archive.
// The reader position is restored. Other formats are left unscanned.
func zipArchiveNames(opts extractStreamOptions) archiveNames {
	if _, ok := opts.Format.(archives.Zip); !ok {
		return archiveNames{}
	}
	enc := scanZipLegacyEncoding(opts.Reader)
	return archiveNames{legacy: enc}
}

func scanZipLegacyEncoding(r io.Reader) encoding.Encoding {
	ra, ok := r.(io.ReaderAt)
	if !ok {
		return nil
	}
	rs, ok := r.(io.Seeker)
	if !ok {
		return nil
	}
	size, err := seekerSize(rs)
	if err != nil || size <= 0 {
		return nil
	}
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return nil
	}
	raw := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		if f == nil {
			continue
		}
		if _, ok := infoZipUnicodeName(f.Extra, f.Name); ok || utf8.ValidString(f.Name) {
			continue
		}
		raw = append(raw, f.Name)
	}
	return detectLegacyEncoding(raw)
}

func seekerSize(rs io.Seeker) (int64, error) {
	cur, err := rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	end, err := rs.Seek(0, io.SeekEnd)
	if err != nil {
		_, _ = rs.Seek(cur, io.SeekStart)
		return 0, err
	}
	_, err = rs.Seek(cur, io.SeekStart)
	return end, err
}

// archiveEntryName returns the relative path to extract one entry to.
func archiveEntryName(f archives.FileInfo, names archiveNames) string {
	raw := f.NameInArchive
	extra := []byte(nil)
	switch h := f.Header.(type) {
	case zip.FileHeader:
		extra = h.Extra
		if h.Name != "" {
			raw = h.Name
		}
	case *zip.FileHeader:
		if h != nil {
			extra = h.Extra
			if h.Name != "" {
				raw = h.Name
			}
		}
	default:
		if utf8.ValidString(raw) {
			return cleanArchivePath(raw)
		}
		return decodeZipName(raw, nil, detectLegacyEncoding([]string{raw}))
	}
	return decodeZipName(raw, extra, names.legacy)
}
