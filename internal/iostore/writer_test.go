package iostore

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// TestContainerRoundtrip: written chunks come back byte identical through the reader, and the geometry matches the
// reference container (version 8, uncompressed blocks, block size 65536).
func TestContainerRoundtrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "z_test_P.utoc")
	payload := bytes.Repeat([]byte("locres-bytes-"), 20000) // several blocks
	var cid ChunkID
	copy(cid[:8], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	cid[11] = 1
	res, err := WriteContainer(WriteOptions{Path: p, ID: ContainerIDFor("z_test_P.pak"), Chunks: []ContainerChunk{{ID: cid, Data: payload}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Blocks < 2 {
		t.Fatalf("expected several blocks, got %d", res.Blocks)
	}
	c, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 8 {
		t.Fatalf("version = %d", c.Version)
	}
	if c.EntriesCount() != 1 {
		t.Fatalf("chunks = %d", c.EntriesCount())
	}
	if int(c.BlockSize) != WriteBlockSize {
		t.Fatalf("block size = %d", c.BlockSize)
	}
	back, err := ChunkData(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, payload) {
		t.Fatal("chunk payload changed in the roundtrip")
	}
	if res.UcasBytes != int64(len(payload)) {
		t.Fatalf("ucas = %d, want %d", res.UcasBytes, len(payload))
	}
}

// TestContainerDeterministic: the same input produces the same bytes, container id included.
func TestContainerDeterministic(t *testing.T) {
	dir := t.TempDir()
	var cid ChunkID
	cid[11] = 1
	opt := WriteOptions{ID: ContainerIDFor("z.pak"), Chunks: []ContainerChunk{{ID: cid, Data: []byte("deterministic payload")}}}
	a, b := filepath.Join(dir, "a.utoc"), filepath.Join(dir, "b.utoc")
	opt.Path = a
	if _, err := WriteContainer(opt); err != nil {
		t.Fatal(err)
	}
	opt.Path = b
	if _, err := WriteContainer(opt); err != nil {
		t.Fatal(err)
	}
	ba, _ := os.ReadFile(a)
	bb, _ := os.ReadFile(b)
	if !bytes.Equal(ba, bb) {
		t.Fatal("container output is not deterministic")
	}
}

// TestContainerRejectsBadInput: an empty chunk or a path without .utoc is refused.
func TestContainerRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	// A container with no chunks is legal; it must still produce a parseable header.
	if res, err := WriteContainer(WriteOptions{Path: filepath.Join(dir, "empty.utoc")}); err != nil {
		t.Fatalf("empty container refused: %v", err)
	} else if res.Chunks != 0 || res.TocBytes != 144 {
		t.Fatalf("empty container wrote %d chunks / %d bytes", res.Chunks, res.TocBytes)
	}
	var cid ChunkID
	cid[11] = 1
	if _, err := WriteContainer(WriteOptions{Path: filepath.Join(dir, "x.toc"), Chunks: []ContainerChunk{{ID: cid, Data: []byte("x")}}}); err == nil {
		t.Fatal("a path without .utoc was accepted")
	}
	if _, err := WriteContainer(WriteOptions{Path: filepath.Join(dir, "y.utoc"), Chunks: []ContainerChunk{{ID: cid, Data: nil}}}); err == nil {
		t.Fatal("an empty chunk was accepted")
	}
}

// TestContainerLayoutAccountsForEveryByte: the header declares counts that must match the file exactly. An
// earlier build declared one perfect-hash seed but wrote no seed array, which shifted every table by four bytes
// while still opening through a lenient reader; a loader would have read the block table four bytes early.
func TestContainerLayoutAccountsForEveryByte(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "z.utoc")
	payload := bytes.Repeat([]byte("block-bytes-"), 30000)
	var cid ChunkID
	cid[11] = 1
	res, err := WriteContainer(WriteOptions{Path: p, ID: ContainerIDFor("z.pak"), Chunks: []ContainerChunk{{ID: cid, Data: payload}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	le := func(o int) int {
		return int(uint32(raw[o]) | uint32(raw[o+1])<<8 | uint32(raw[o+2])<<16 | uint32(raw[o+3])<<24)
	}
	hdr, chunks, blocks := le(20), le(24), le(28)
	dirSize, seeds, without := le(48), le(84), le(96)
	base := hdr + chunks*22 + seeds*4 + without*4 + blocks*12 + dirSize
	if len(raw)-base != chunks*24 {
		t.Fatalf("trailer is %d bytes for %d chunks, want %d", len(raw)-base, chunks, chunks*24)
	}
	if seeds != 0 {
		t.Fatalf("writer declared %d perfect-hash seeds but writes no seed array", seeds)
	}
	if res.Blocks != blocks {
		t.Fatalf("reported %d blocks, header declares %d", res.Blocks, blocks)
	}
	// The block table the reader sees must describe contiguous ranges inside the ucas.
	c, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for i, e := range c.Entries() {
		if e.Offset != total {
			t.Fatalf("chunk %d starts at %d, want %d", i, e.Offset, total)
		}
		for j, b := range e.Blocks {
			if int64(b.Offset) != total+int64(j)*WriteBlockSize && j > 0 {
				t.Fatalf("chunk %d block %d offset %d is not contiguous", i, j, b.Offset)
			}
			if b.Compressed != b.Uncompressed {
				t.Fatalf("chunk %d block %d is not stored uncompressed", i, j)
			}
		}
		total += e.Length
	}
	if total != int64(len(payload)) {
		t.Fatalf("chunks cover %d bytes, payload is %d", total, len(payload))
	}
}

// TestTocFieldOffsets pins the v8 header fields to their documented offsets, so a value can never be written into a
// neighbouring field and still look plausible because both happen to be small numbers.
func TestTocFieldOffsets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fields.utoc")
	cid := ContainerIDFor("z_fields_P.pak")
	ch := ContainerChunk{ID: NewChunkID(0x0102030405060708, ChunkContainerHeader), Data: NewEmptyContainerHeader(0x0102030405060708).Serialize()}
	if _, err := WriteContainer(WriteOptions{Path: path, ID: cid, Chunks: []ContainerChunk{ch}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	u32 := func(off int) uint32 { return binary.LittleEndian.Uint32(b[off:]) }
	if got := u32(0x10); got != 8 {
		t.Fatalf("version at 0x10 = %d", got)
	}
	if got := u32(0x14); got != 144 {
		t.Fatalf("toc header size at 0x14 = %d", got)
	}
	if got := u32(0x20); got != 12 {
		t.Fatalf("block entry size at 0x20 = %d", got)
	}
	if got := u32(0x2C); got != WriteBlockSize {
		t.Fatalf("block size at 0x2C = %d", got)
	}
	if got := u32(0x30); got != 0 {
		t.Fatalf("directory index size at 0x30 = %d, want 0 while the container is not Indexed", got)
	}
	if got := u32(0x34); got != 1 {
		t.Fatalf("partition count at 0x34 = %d", got)
	}
	if b[0x38] != cid[0] || b[0x3F] != cid[7] {
		t.Fatalf("container id at 0x38 = %s", b[0x38:0x40])
	}
	if b[0x50] != 0 {
		t.Fatalf("container flags at 0x50 = 0x%02x, want 0", b[0x50])
	}
	if got := binary.LittleEndian.Uint64(b[0x58:]); got != 0xFFFFFFFFFFFFFFFF {
		t.Fatalf("partition size at 0x58 = 0x%x", got)
	}
	// The flag byte and the directory index must stay consistent: Indexed implies a payload, and a payload implies
	// the flag. Neither is set here.
	if b[0x50]&0x08 != 0 != (u32(0x30) > 0) {
		t.Fatal("Indexed flag and directory index disagree")
	}
}
