package filesystem

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io/fs"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	kzip "github.com/klauspost/compress/zip"
	"github.com/mholt/archives"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

func TestCleanArchivePath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"foo\\bar", "foo/bar"},
		{"C:/foo/bar", "foo/bar"},
		{"C:\\foo\\bar", "foo/bar"},
		{"/foo/bar", "foo/bar"},
		{"foo/bar/", "foo/bar/"},
		{"foo\x00bar", "foobar"},
		{"", ""},
		{"foo/../../etc/passwd", "../etc/passwd"},
		{"世界\\文件夹.txt", "世界/文件夹.txt"},
	}
	for _, tc := range cases {
		if got := cleanArchivePath(tc.in); got != tc.want {
			t.Errorf("cleanArchivePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDecodeZipNameUTF8Passthrough(t *testing.T) {
	for _, name := range []string{"中文.txt", "ファイル.txt", "readme.txt"} {
		got := decodeZipName(name, nil, simplifiedchinese.GB18030)
		if got != name {
			t.Errorf("decodeZipName(%q) = %q", name, got)
		}
	}
}

func TestInfoZipUnicodePathOverridesLegacy(t *testing.T) {
	raw := mustEncode(t, simplifiedchinese.GB18030, "旧名字.txt")
	extra := unicodePathExtra([]byte(raw), "河北每日.txt")
	got := decodeZipName(raw, extra, simplifiedchinese.GB18030)
	if got != "河北每日.txt" {
		t.Fatalf("unicode extra: got %q", got)
	}

	bad := append([]byte(nil), extra...)
	bad[5] ^= 0xff
	got = decodeZipName(raw, bad, simplifiedchinese.GB18030)
	if got != "旧名字.txt" {
		t.Fatalf("bad crc: got %q", got)
	}

	prefixed := append([]byte{0x01, 0x99, 0x02, 0x00, 0x00, 0x00}, extra...)
	got = decodeZipName(raw, prefixed, nil)
	if got != "河北每日.txt" {
		t.Fatalf("prefixed extra: got %q", got)
	}
}

func TestDetectLegacyZipNames(t *testing.T) {
	cases := []struct {
		name string
		enc  encoding.Encoding
		in   string
		want string
	}{
		{"gbk production path", simplifiedchinese.GB18030, "world/customnpcs/dialogs/河北每日/对话.txt", "world/customnpcs/dialogs/河北每日/对话.txt"},
		{"gbk backslash", simplifiedchinese.GB18030, "world\\customnpcs\\dialogs\\河北每日\\对话.txt", "world/customnpcs/dialogs/河北每日/对话.txt"},
		{"gbk drive", simplifiedchinese.GB18030, "C:\\世界\\文件夹.txt", "世界/文件夹.txt"},
		{"gbk ascii prefix", simplifiedchinese.GB18030, "readme河北.txt", "readme河北.txt"},
		{"big5", traditionalchinese.Big5, "臺灣繁體.txt", "臺灣繁體.txt"},
		{"shift-jis", japanese.ShiftJIS, "ファイル.txt", "ファイル.txt"},
		{"euc-jp", japanese.EUCJP, "ファイル.txt", "ファイル.txt"},
		{"euc-kr", korean.EUCKR, "한글.txt", "한글.txt"},
		{"windows-1252", charmap.Windows1252, "docs/Müller.txt", "docs/Müller.txt"},
		{"windows-1251", charmap.Windows1251, "Привет.txt", "Привет.txt"},
		{"koi8-r", charmap.KOI8R, "Привет.txt", "Привет.txt"},
		{"cp866", charmap.CodePage866, "Привет.txt", "Привет.txt"},
		{"cp437", charmap.CodePage437, "año.txt", "año.txt"},
		{"cp850", charmap.CodePage850, "Müller.txt", "Müller.txt"},
		{"windows-1250", charmap.Windows1250, "Zażółć.txt", "Zażółć.txt"},
		{"windows-1253", charmap.Windows1253, "Ελληνικά.txt", "Ελληνικά.txt"},
		{"windows-1255", charmap.Windows1255, "שלום.txt", "שלום.txt"},
		{"windows-1256", charmap.Windows1256, "مرحبا.txt", "مرحبا.txt"},
		{"windows-874", charmap.Windows874, "สวัสดี.txt", "สวัสดี.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := mustEncode(t, tc.enc, tc.in)
			if utf8.ValidString(raw) {
				t.Fatalf("encoded %q is valid UTF-8 (%x); test would not exercise detection", tc.in, raw)
			}
			got := decodeZipName(raw, nil, detectLegacyEncoding([]string{raw}))
			if got != tc.want {
				t.Fatalf("got %q want %q raw %x", got, tc.want, raw)
			}
		})
	}
}

func TestNormalizeZipNamesWalk(t *testing.T) {
	gbkDir := mustEncode(t, simplifiedchinese.GB18030, "world/customnpcs/dialogs/河北每日/")
	gbkFile := mustEncode(t, simplifiedchinese.GB18030, "world/customnpcs/dialogs/河北每日/对话.txt")
	data, err := buildRawZip([]rawZipEntry{
		{name: gbkDir, dir: true},
		{name: gbkFile, body: []byte("dialog-body")},
	})
	if err != nil {
		t.Fatal(err)
	}

	broken, err := kzip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	err = fs.WalkDir(broken, ".", func(p string, d fs.DirEntry, err error) error { return err })
	if err == nil || !strings.Contains(err.Error(), "invalid argument") {
		t.Fatalf("unnormalized walk error = %v", err)
	}

	zr, err := kzip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	normalizeZipNames(zr.File)
	var names []string
	err = fs.WalkDir(zr, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !utf8.ValidString(p) {
			t.Errorf("walked invalid UTF-8 path %q", p)
		}
		if p != "." {
			names = append(names, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wantFile := "world/customnpcs/dialogs/河北每日/对话.txt"
	if !containsString(names, wantFile) {
		t.Fatalf("walked %#v", names)
	}
}

func TestNormalizeZipNamesUTF8WithoutFlag(t *testing.T) {
	data, err := buildRawZip([]rawZipEntry{
		{name: "中文/文件.txt", body: []byte("ok"), utf8: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Force the UTF-8 flag off while keeping the UTF-8 bytes.
	data = clearZipUTF8Flag(t, data)

	zr, err := kzip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !zr.File[0].NonUTF8 {
		t.Fatal("expected NonUTF8 for UTF-8 bytes stored without the flag")
	}
	normalizeZipNames(zr.File)
	if zr.File[0].Name != "中文/文件.txt" {
		t.Fatalf("name = %q", zr.File[0].Name)
	}
}

func TestArchiveEntryNameZipHeader(t *testing.T) {
	raw := mustEncode(t, simplifiedchinese.GB18030, "河北每日.txt")
	names := archiveNames{legacy: detectLegacyEncoding([]string{raw})}
	fi := archives.FileInfo{
		NameInArchive: "ignored",
		Header:        kzip.FileHeader{Name: raw, NonUTF8: true},
	}
	if got := archiveEntryName(fi, names); got != "河北每日.txt" {
		t.Fatalf("value header: %q", got)
	}
	hdr := &kzip.FileHeader{Name: raw, NonUTF8: true}
	fi.Header = hdr
	if got := archiveEntryName(fi, names); got != "河北每日.txt" {
		t.Fatalf("pointer header: %q", got)
	}
}

type rawZipEntry struct {
	name  string
	body  []byte
	dir   bool
	extra []byte
	// utf8 selects the ZIP UTF-8 flag. Raw legacy names leave it false.
	utf8 bool
}

func buildRawZip(entries []rawZipEntry) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		name := e.name
		if e.dir && !strings.HasSuffix(name, "/") {
			name += "/"
		}
		h := &zip.FileHeader{
			Name:    name,
			Extra:   e.extra,
			NonUTF8: !e.utf8,
		}
		if e.dir {
			h.SetMode(os.ModeDir | 0o755)
		} else {
			h.Method = zip.Deflate
			h.SetMode(0o644)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if !e.dir {
			if _, err := w.Write(e.body); err != nil {
				return nil, err
			}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func mustEncode(t *testing.T, enc encoding.Encoding, s string) string {
	t.Helper()
	out, err := enc.NewEncoder().String(s)
	if err != nil {
		t.Fatalf("encode %q: %v", s, err)
	}
	return out
}

func unicodePathExtra(raw []byte, unicodeName string) []byte {
	payload := make([]byte, 1+4+len(unicodeName))
	payload[0] = 1
	binary.LittleEndian.PutUint32(payload[1:], crc32.ChecksumIEEE(raw))
	copy(payload[5:], unicodeName)
	extra := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint16(extra[0:], zipUnicodePathExtra)
	binary.LittleEndian.PutUint16(extra[2:], uint16(len(payload)))
	copy(extra[4:], payload)
	return extra
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// clearZipUTF8Flag clears general-purpose bit 11 on every central directory
// header so a UTF-8 name is stored the way old archivers stored it.
func clearZipUTF8Flag(t *testing.T, data []byte) []byte {
	t.Helper()
	out := append([]byte(nil), data...)
	// ZIP end of central directory is at the end for these small archives.
	sig := []byte{0x50, 0x4b, 0x05, 0x06}
	idx := bytes.LastIndex(out, sig)
	if idx < 0 || idx+20 > len(out) {
		t.Fatal("missing end of central directory")
	}
	count := int(binary.LittleEndian.Uint16(out[idx+8:]))
	off := int(binary.LittleEndian.Uint32(out[idx+16:]))
	const cenSig = 0x02014b50
	for n := 0; n < count; n++ {
		if off+46 > len(out) || binary.LittleEndian.Uint32(out[off:]) != cenSig {
			t.Fatal("bad central directory")
		}
		flags := binary.LittleEndian.Uint16(out[off+8:])
		binary.LittleEndian.PutUint16(out[off+8:], flags&^0x800)
		nameLen := int(binary.LittleEndian.Uint16(out[off+28:]))
		extraLen := int(binary.LittleEndian.Uint16(out[off+30:]))
		commentLen := int(binary.LittleEndian.Uint16(out[off+32:]))
		off += 46 + nameLen + extraLen + commentLen
	}
	// Local headers carry the same flag. Clear those too.
	local := []byte{0x50, 0x4b, 0x03, 0x04}
	for at := 0; ; {
		i := bytes.Index(out[at:], local)
		if i < 0 {
			break
		}
		i += at
		if i+8 >= len(out) {
			break
		}
		flags := binary.LittleEndian.Uint16(out[i+6:])
		binary.LittleEndian.PutUint16(out[i+6:], flags&^0x800)
		at = i + 4
	}
	return out
}
