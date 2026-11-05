package pak

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"unburger/internal/oodle"
)

func projectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// writeArchive builds a minimal archive with the inline-entry layout the reader expects.
func writeArchive(t *testing.T, path string, payloads [][]byte) {
	t.Helper()
	var body bytes.Buffer
	for _, p := range payloads {
		pos := body.Len()
		hdr := make([]byte, HeaderLen)
		binary.LittleEndian.PutUint64(hdr[0:], uint64(pos))
		binary.LittleEndian.PutUint64(hdr[8:], uint64(len(p)))
		binary.LittleEndian.PutUint64(hdr[16:], uint64(len(p)))
		sum := sha256.Sum256(p)
		copy(hdr[28:], sum[:20])
		body.Write(hdr)
		body.Write(p)
		for body.Len()%16 != 0 {
			body.WriteByte(0)
		}
	}
	if err := os.WriteFile(path, body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWalkAndReadStored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mini.pak")
	files := [][]byte{[]byte("first payload"), []byte(""), []byte("third, longer payload \x00\x01\x02")}
	writeArchive(t, path, files)

	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if len(r.Entries()) != len(files) {
		t.Fatalf("walked %d entries, want %d", len(r.Entries()), len(files))
	}
	for i, e := range r.Entries() {
		got, err := r.Read(e, nil)
		if err != nil {
			t.Fatalf("entry %d: %v", i, err)
		}
		if !bytes.Equal(got, files[i]) {
			t.Fatalf("entry %d payload mismatch: %q", i, got)
		}
	}
}

func TestTruncatedArchiveStopsCleanly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cut.pak")
	writeArchive(t, path, [][]byte{[]byte("aaaaaaaaaa"), []byte("bbbbbbbbbb")})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw[:40], 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if n := len(r.Entries()); n != 0 {
		t.Fatalf("truncated archive yielded %d entries", n)
	}
}

// TestRealArchiveRegression re-derives the extraction results that were first obtained
// with a throw-away prototype: 37 localisation archives, six of them stored uncompressed.
func TestRealArchiveRegression(t *testing.T) {
	root := projectRoot(t)
	path := filepath.Join(root, ".local/originals/switch/romfs/FFRS/Content/Paks/FFRS-Switch.pak")
	if _, err := os.Stat(path); err != nil {
		t.Skip("no Switch archive on this host")
	}
	lib := filepath.Join(root, ".local/toolchain/vendor/oodle/liboo2corelinux64.so.9")
	var decode Decoder
	if _, err := os.Stat(lib); err == nil {
		if err := oodle.Load(lib); err != nil {
			t.Fatalf("load oodle: %v", err)
		}
		decode = func(comp []byte, raw int) ([]byte, error) { return oodle.Decode(lib, comp, raw) }
	}

	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if n := len(r.Entries()); n < 2000 {
		t.Fatalf("walked only %d entries", n)
	}

	magic := []byte{0x0e, 0x14, 0x74, 0x75, 0x67, 0x4a, 0x03, 0xfc, 0x4a, 0x15, 0x90, 0x9d, 0xc3, 0x37, 0x7f, 0x1b}
	found := map[string]bool{}
	total := 0
	for _, e := range r.Entries() {
		if e.BlockCount > 0 && decode == nil {
			continue
		}
		data, err := r.Read(e, decode)
		if err != nil || len(data) < 16 || !bytes.Equal(data[:16], magic) {
			continue
		}
		total++
		sum := sha256.Sum256(data)
		found[hex.EncodeToString(sum[:])[:16]] = true
	}
	if decode == nil {
		if len(found) < 5 {
			t.Fatalf("stored localisation archives found: %d", len(found))
		}
		return
	}
	// 37 localisation archives are shipped, but several language pairs are byte-identical,
	// so the content-addressed set is smaller than the entry count.
	if total != 37 {
		t.Fatalf("localisation entries found: %d, want 37", total)
	}
	if len(found) != 32 {
		t.Fatalf("distinct localisation archives: %d, want 32", len(found))
	}
	for _, want := range []string{
		"dcee72b9789f4d9f", // dialogue, Japanese
		"af0661cf51d98760", // dialogue, English
		"8d3986ffe06ed42f", // terms, Japanese
		"aaa4120d84f3c2b9", // terms, English
		"9dfb7dc9d88d812e", // ui, Japanese
		"991867cc9705c1dd", // ui, English
	} {
		if !found[want] {
			t.Fatalf("expected localisation archive %s not found", want)
		}
	}
}
