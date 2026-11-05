package oodle

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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

func loadVendor(t *testing.T) string {
	t.Helper()
	base := filepath.Join(projectRoot(t), ".local/toolchain/vendor/oodle")
	for _, name := range []string{"liboo2corelinux64.so.9", "liboo2corelinux64.so"} {
		path := filepath.Join(base, name)
		if _, err := os.Stat(path); err == nil {
			if err := Load(path); err != nil {
				t.Fatalf("load %s: %v", path, err)
			}
			return path
		}
	}
	t.Skip("no vendored Oodle library; decode path not exercised")
	return ""
}

func TestRoundTrip(t *testing.T) {
	path := loadVendor(t)
	raw := bytes.Repeat([]byte("Kwestia dialogowa 0012 / セリフ / line\n"), 4096)
	comp, err := Compress(path, raw, 6)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if len(comp) >= len(raw) {
		t.Fatalf("compressed fixture is not smaller: %d >= %d", len(comp), len(raw))
	}
	out, err := Decode(path, comp, len(raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatal("round trip mismatch")
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	path := loadVendor(t)
	if _, err := Decode(path, bytes.Repeat([]byte{0}, 512), 4096); err == nil {
		t.Fatal("garbage accepted as an Oodle block")
	}
	if _, err := Decode(path, []byte{1, 2, 3}, 1<<28); err == nil {
		t.Fatal("implausible raw size accepted")
	}
}

func TestMissingLibraryIsAnError(t *testing.T) {
	if err := Load("/nonexistent/liboo2core.so"); err == nil {
		t.Fatal("missing library accepted")
	}
}
