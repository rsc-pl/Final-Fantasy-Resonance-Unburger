package pak

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func zlibDecode(t *testing.T) PayloadDecoder {
	t.Helper()
	return func(comp []byte, raw int) ([]byte, error) {
		r, err := zlib.NewReader(bytes.NewReader(comp))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		b, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		if len(b) != raw {
			t.Fatalf("block decoded to %d bytes, want %d", len(b), raw)
		}
		return b, nil
	}
}

// TestCompressedRoundtrip reproduces the shape of the working reference archive: one zlib-compressed entry in
// blocks, the footer naming the method, and a payload that decodes back byte for byte.
func TestCompressedRoundtrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "z_test_P.pak")
	payload := bytes.Repeat([]byte("UNBURGER-LOCRES-BLOCK-"), 9000) // spans several blocks
	res, err := WritePatch(p, PatchOptions{
		MountPoint: DefaultMountPoint, UseZlib: true, BlockSize: 126976, CompressionName: "Zlib",
		Entries: []PatchEntry{{Path: VirtualPath, Data: payload}},
	})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := ReadV11(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Problems) > 0 {
		t.Fatalf("reader problems: %v", idx.Problems)
	}
	if idx.Compressed != 1 || idx.Files[0].Method != 1 {
		t.Fatalf("entry is not compressed: %d/%d", idx.Compressed, idx.Files[0].Method)
	}
	if idx.Files[0].Uncompressed != int64(len(payload)) {
		t.Fatalf("uncompressed = %d, want %d", idx.Files[0].Uncompressed, len(payload))
	}
	if res.Entries[0].Size >= int32(len(payload)) {
		t.Fatalf("compressed size %d is not smaller than the payload", res.Entries[0].Size)
	}
	back, err := ReadV11Decoded(p, VirtualPath, zlibDecode(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, payload) {
		t.Fatal("decoded payload differs from what was written")
	}

	// Header geometry: block count at +48, pairs measured from the entry start, payload right after the table.
	raw, _ := os.ReadFile(p)
	off := idx.Files[0].Offset
	blocks := int(int32(binary.LittleEndian.Uint32(raw[off+48:])))
	if blocks < 2 {
		t.Fatalf("expected several blocks, got %d", blocks)
	}
	headerLen := 57 + 16*blocks
	first := binary.LittleEndian.Uint64(raw[off+52:])
	last := binary.LittleEndian.Uint64(raw[off+52+int64(16*(blocks-1))+8:])
	if first != uint64(headerLen) {
		t.Fatalf("first block starts at %d, want %d", first, headerLen)
	}
	if int64(last) != int64(headerLen)+idx.Files[0].Size {
		t.Fatalf("last block ends at %d, want %d", last, int64(headerLen)+idx.Files[0].Size)
	}
	compressed := raw[off+int64(headerLen) : off+int64(headerLen)+idx.Files[0].Size]
	if got, want := idx.Files[0].SHA1, fmt.Sprintf("%x", sha1.Sum(compressed)); got != want {
		t.Fatalf("reader payload sha1 = %q, want compressed-payload %q", got, want)
	}
	if idx.Files[0].SHA1 != res.Entries[0].SHA1 {
		t.Fatalf("reader sha1 %q != writer sha1 %q", idx.Files[0].SHA1, res.Entries[0].SHA1)
	}

	// Footer method table names the codec.
	tail := raw[len(raw)-160 : len(raw)-128]
	if got := string(bytes.TrimRight(tail, "\x00")); got != "Zlib" {
		t.Fatalf("footer method slot = %q", got)
	}
}

// TestCompressedDeterministic: two writes of the same input produce the same bytes.
func TestCompressedDeterministic(t *testing.T) {
	dir := t.TempDir()
	opt := PatchOptions{
		MountPoint: DefaultMountPoint, UseZlib: true, BlockSize: 126976, CompressionName: "Zlib",
		Entries: []PatchEntry{{Path: VirtualPath, Data: bytes.Repeat([]byte("deterministic"), 5000)}},
	}
	a, b := filepath.Join(dir, "a.pak"), filepath.Join(dir, "b.pak")
	if _, err := WritePatch(a, opt); err != nil {
		t.Fatal(err)
	}
	if _, err := WritePatch(b, opt); err != nil {
		t.Fatal(err)
	}
	ba, _ := os.ReadFile(a)
	bb, _ := os.ReadFile(b)
	if !bytes.Equal(ba, bb) {
		t.Fatal("compressed output is not deterministic")
	}
}

// TestCorruptFooterIsRejected: a damaged index hash is reported, never silently accepted.
func TestCorruptFooterIsRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "z.pak")
	if _, err := WritePatch(p, PatchOptions{
		MountPoint: DefaultMountPoint, UseZlib: true, BlockSize: 126976, CompressionName: "Zlib",
		Entries: []PatchEntry{{Path: VirtualPath, Data: []byte("payload")}},
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	raw[len(raw)-180] ^= 0xFF // first byte of the footer's index hash (17 header bytes + magic + version + offset + size)
	bad := filepath.Join(dir, "bad.pak")
	if err := os.WriteFile(bad, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := ReadV11(bad)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Problems) == 0 {
		t.Fatal("a damaged index hash was accepted")
	}
}

// TestCorruptBlockOffsetIsRejected: an impossible block range fails the decode instead of returning garbage.
func TestCorruptBlockOffsetIsRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "z.pak")
	if _, err := WritePatch(p, PatchOptions{
		MountPoint: DefaultMountPoint, UseZlib: true, BlockSize: 126976, CompressionName: "Zlib",
		Entries: []PatchEntry{{Path: VirtualPath, Data: bytes.Repeat([]byte("x"), 200000)}},
	}); err != nil {
		t.Fatal(err)
	}
	idx, err := ReadV11(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	off := idx.Files[0].Offset
	binary.LittleEndian.PutUint64(raw[off+52:], 1<<40) // block starts far outside the file
	bad := filepath.Join(dir, "bad.pak")
	if err := os.WriteFile(bad, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadV11Decoded(bad, VirtualPath, zlibDecode(t)); err == nil {
		t.Fatal("an impossible block offset was decoded")
	}
}

// TestCorruptVirtualPathIsRejected: asking for a path the archive does not carry fails.
func TestCorruptVirtualPathIsRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "z.pak")
	if _, err := WritePatch(p, PatchOptions{
		MountPoint: DefaultMountPoint, UseZlib: true, BlockSize: 126976, CompressionName: "Zlib",
		Entries: []PatchEntry{{Path: VirtualPath, Data: []byte("payload")}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadV11Decoded(p, "FFRS/Content/Localization/Game/fr/Game.locres", zlibDecode(t)); err == nil {
		t.Fatal("a missing virtual path was returned")
	}
}
