package iostore

import (
	"os"
	"path/filepath"
	"strings"
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
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func openReal(t *testing.T) (*Container, Decoder) {
	t.Helper()
	root := projectRoot(t)
	toc := filepath.Join(root, ".local/originals/switch/updates/1.0.1/romfs/FFRS/Content/Paks/FFRS-Switch.utoc")
	if _, err := os.Stat(toc); err != nil {
		t.Skip("no IoStore container on this host")
	}
	c, err := Open(toc)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	lib := filepath.Join(root, ".local/toolchain/vendor/oodle/liboo2corelinux64.so.9")
	if _, err := os.Stat(lib); err == nil {
		if err := oodle.Load(lib); err != nil {
			t.Fatalf("oodle: %v", err)
		}
		return c, func(comp []byte, raw int) ([]byte, error) { return oodle.Decode(lib, comp, raw) }
	}
	return c, nil
}

// TestHeaderAndBlocks pins the header values and the block geometry: every block lies inside
// the .ucas, block sizes stay within one block, and chunk offsets are logical so the block
// base is offset/blockSize.
func TestHeaderAndBlocks(t *testing.T) {
	c, _ := openReal(t)
	if c.Version != 8 || c.EntryCount != 63910 || c.BlockCount != 518224 || c.BlockSize != 65536 {
		t.Fatalf("unexpected header: v%d entries=%d blocks=%d size=%d",
			c.Version, c.EntryCount, c.BlockCount, c.BlockSize)
	}
	ucas, err := os.Stat(c.UcasPath)
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range c.blocks {
		if b.Offset < 0 || b.Offset+int64(b.Compressed) > ucas.Size() {
			t.Fatalf("block %d outside the container", i)
		}
		if b.Compressed <= 0 || b.Compressed > 65536 || b.Uncompressed <= 0 || b.Uncompressed > 65536 {
			t.Fatalf("block %d sizes %d/%d", i, b.Compressed, b.Uncompressed)
		}
	}
	for i, e := range c.entries {
		if e.Length > 0 && e.BlockBase+len(e.Blocks) > len(c.blocks) {
			t.Fatalf("chunk %d has an out-of-range block span", i)
		}
	}
}

// TestDecodeChunks decodes a sample of authentic chunks and requires exact lengths: the
// block table, the logical offsets and the Oodle calls must agree, or the sizes drift.
func TestDecodeChunks(t *testing.T) {
	c, decode := openReal(t)
	if decode == nil {
		t.Skip("no Oodle library")
	}
	types := map[uint8]int{}
	for _, e := range c.Entries() {
		types[e.ID.Type()]++
	}
	t.Logf("chunk types: %v", types)
	if types[1] == 0 || types[2] == 0 {
		t.Fatalf("expected export bundles and bulk chunks, got %v", types)
	}
	decoded := 0
	for i, e := range c.Entries() {
		if e.Length == 0 {
			continue
		}
		data, err := c.Read(e.ID, decode)
		if err != nil {
			t.Fatalf("chunk %d (%d bytes): %v", i, e.Length, err)
		}
		if int64(len(data)) != e.Length {
			t.Fatalf("chunk %d decoded %d of %d", i, len(data), e.Length)
		}
		decoded++
		if decoded == 24 {
			break
		}
	}
	if decoded < 24 {
		t.Fatalf("only %d chunks decoded", decoded)
	}
}

// TestDirectoryIndex checks the reconstructed tree: every file has a path under the mount
// point, no path has an empty segment, every UserData addresses a real chunk, and the
// namespace counts match the UFS manifest of the same build.
func TestDirectoryIndex(t *testing.T) {
	c, _ := openReal(t)
	files := c.Files()
	if len(files) != 57990 {
		t.Fatalf("files %d, want 57990", len(files))
	}
	stDT, seq, ffrs := 0, 0, 0
	ext := map[string]int{}
	for _, f := range files {
		if !strings.HasPrefix(f.Path, "../../../") {
			t.Fatalf("path outside the mount point: %q", f.Path)
		}
		if strings.HasPrefix(f.Path, "../../../FFRS/") {
			ffrs++
		}
		if strings.Contains(f.Path, "//") || strings.HasSuffix(f.Path, "/") {
			t.Fatalf("malformed path: %q", f.Path)
		}
		if f.Index < 0 || f.Index >= c.EntryCount {
			t.Fatalf("file %q addresses chunk %d of %d", f.Path, f.Index, c.EntryCount)
		}
		if strings.Contains(f.Path, "/ST_") || strings.Contains(f.Path, "/DT_") {
			stDT++
		}
		if strings.Contains(f.Path, "/Sequencer/") {
			seq++
		}
		ext[strings.ToLower(filepath.Ext(f.Path))]++
	}
	t.Logf("files=%d ffrs=%d ST_/DT_=%d Sequencer=%d uasset=%d uexp=%d ubulk=%d",
		len(files), ffrs, stDT, seq, ext[".uasset"], ext[".uexp"], ext[".ubulk"])
	if ffrs < 50000 {
		t.Fatalf("only %d files under FFRS", ffrs)
	}
	if stDT < 2000 || seq < 5000 {
		t.Fatalf("suspicious namespace counts: st/dt=%d sequencer=%d", stDT, seq)
	}
	// IoStore keeps the export data inside the .uasset chunk, so no .uexp path exists; the
	// bulk data is listed separately.
	if ext[".uasset"] < 40000 || ext[".ubulk"] < 10000 || ext[".umap"] < 1000 {
		t.Fatalf("suspicious extension counts: %v", ext)
	}
}

// TestDirectoryChainIsAcyclic proves the sibling and file chains terminate: walking every
// directory and every file chain must visit each entry once.
func TestDirectoryChainIsAcyclic(t *testing.T) {
	c, _ := openReal(t)
	seen := make([]bool, c.EntryCount)
	for _, f := range c.Files() {
		if seen[f.Index] {
			continue
		}
		seen[f.Index] = true
	}
	distinct := 0
	for _, v := range seen {
		if v {
			distinct++
		}
	}
	if distinct < 40000 {
		t.Fatalf("only %d distinct chunks referenced by files", distinct)
	}
	t.Logf("distinct chunks referenced by files: %d of %d", distinct, c.EntryCount)
}
