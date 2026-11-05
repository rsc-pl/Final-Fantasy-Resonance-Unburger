package iostore

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// TestEmptyHeaderLayout pins the 64-byte shape: every array empty, three zero counts, serial info pointing at the
// four reserved bytes and a zero soft-reference flag.
func TestEmptyHeaderLayout(t *testing.T) {
	h := NewEmptyContainerHeader(0x79591B2E930B8D0B)
	b := h.Serialize()
	if len(b) != 64 {
		t.Fatalf("empty header is %d bytes, want 64", len(b))
	}
	if binary.LittleEndian.Uint32(b[0:]) != ContainerHeaderMagic {
		t.Fatal("magic is wrong")
	}
	if binary.LittleEndian.Uint32(b[4:]) != 5 {
		t.Fatal("version is wrong")
	}
	if binary.LittleEndian.Uint64(b[8:]) != 0x79591B2E930B8D0B {
		t.Fatal("container id is wrong")
	}
	for _, off := range []int{16, 20, 24, 28, 32, 36, 40} {
		if binary.LittleEndian.Uint32(b[off:]) != 0 {
			t.Fatalf("offset %d is not zero", off)
		}
	}
	if got := int64(binary.LittleEndian.Uint64(b[44:])); got != 60 {
		t.Fatalf("soft reference offset = %d, want 60", got)
	}
	if got := int64(binary.LittleEndian.Uint64(b[52:])); got != 4 {
		t.Fatalf("soft reference size = %d, want 4", got)
	}
	if binary.LittleEndian.Uint32(b[60:]) != 0 {
		t.Fatal("soft reference flag is set")
	}
	if h.SoftRefOffset+int64(h.SoftRefSize) != int64(len(b)) {
		t.Fatal("serial info does not cover the tail of the header")
	}
	back, err := ParseContainerHeader(b)
	if err != nil {
		t.Fatal(err)
	}
	if back.ContainerID != h.ContainerID || len(back.PackageIDs) != 0 || back.HasSoftRefs {
		t.Fatal("empty header did not read back")
	}
	if !bytes.Equal(back.Serialize(), b) {
		t.Fatal("empty header does not round-trip")
	}
}

// TestHeaderRejectsDamage: a wrong magic, a truncated header and an impossible serial info are refused.
func TestHeaderRejectsDamage(t *testing.T) {
	good := NewEmptyContainerHeader(1).Serialize()
	bad := make([]byte, len(good))
	copy(bad, good)
	bad[0] ^= 0xFF
	if _, err := ParseContainerHeader(bad); err == nil {
		t.Fatal("bad magic accepted")
	}
	if _, err := ParseContainerHeader(good[:30]); err == nil {
		t.Fatal("truncated header accepted")
	}
	bad = append([]byte{}, good...)
	binary.LittleEndian.PutUint64(bad[44:], 1<<40)
	if _, err := ParseContainerHeader(bad); err == nil {
		t.Fatal("impossible serial info accepted")
	}
}

// TestReferenceHeaderRoundTrip reads the real 38-package header, re-emits it and requires identical bytes.
func TestReferenceHeaderRoundTrip(t *testing.T) {
	root := projectRoot(t)
	toc := filepath.Join(root, ".local/reference-mods/ref-ru/atmosphere/contents/0100606026F12000/romfs/FFRS/Content/Paks/z_ffrs_ru_P.utoc")
	if _, err := os.Stat(toc); err != nil {
		t.Skip("reference container not present")
	}
	c, err := Open(toc)
	if err != nil {
		t.Fatal(err)
	}
	ucas, err := os.ReadFile(c.UcasPath)
	if err != nil {
		t.Fatal(err)
	}
	var payload []byte
	var wantID ChunkID
	for i, e := range c.Entries() {
		if TypeOf(e.ID) != ChunkContainerHeader {
			continue
		}
		wantID = e.ID
		for _, b := range e.Blocks {
			payload = append(payload, ucas[b.Offset:b.Offset+int64(b.Compressed)]...)
		}
		payload = payload[:e.Length]
		_ = i
		break
	}
	if payload == nil {
		t.Fatal("reference container has no container header chunk")
	}
	h, err := ParseContainerHeader(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.PackageIDs) != 38 {
		t.Fatalf("package ids = %d, want 38", len(h.PackageIDs))
	}
	t.Logf("reference serial info: offset=%d size=%d flag-after-window=%v middle=%d",
		h.SoftRefOffset, h.SoftRefSize, h.FlagAfterWindow, len(h.Middle))
	if wantID[11] != byte(ChunkContainerHeader) {
		t.Fatal("header chunk id does not carry type 6")
	}
	if !bytes.Equal(h.Serialize(), payload) {
		t.Fatalf("reference header does not round-trip (%d bytes in, %d out)", len(payload), len(h.Serialize()))
	}
}

// TestEmptyHeaderV5ReferenceLayout pins the exact bytes the engine's serializer produces for an empty header:
// offset 60, size 4, the four-byte flag inside that window, and nothing after it.
func TestEmptyHeaderV5ReferenceLayout(t *testing.T) {
	b := NewEmptyContainerHeader(0x1122334455667788).Serialize()
	if len(b) != 64 {
		t.Fatalf("length = %d, want 64", len(b))
	}
	off := int64(binary.LittleEndian.Uint64(b[44:]))
	size := int64(binary.LittleEndian.Uint64(b[52:]))
	if off != 60 || size != 4 {
		t.Fatalf("serial info = %d+%d, want 60+4", off, size)
	}
	if off+size != int64(len(b)) {
		t.Fatalf("serial info %d+%d does not end at %d", off, size, len(b))
	}
	h, err := ParseContainerHeader(b)
	if err != nil {
		t.Fatal(err)
	}
	if h.FlagAfterWindow {
		t.Fatal("empty header was read as the trailing-flag variant")
	}
	if !bytes.Equal(h.Serialize(), b) {
		t.Fatal("round trip differs")
	}
}

// TestRejectInvalidSoftReferencesSize: a header that declares a zero-length window while still carrying a flag is
// inconsistent and must be refused, not silently accepted.
func TestRejectInvalidSoftReferencesSize(t *testing.T) {
	b := NewEmptyContainerHeader(1).Serialize()
	binary.LittleEndian.PutUint64(b[52:], 0)
	if _, err := ParseContainerHeader(b); err == nil {
		t.Fatal("a zero-length soft-reference window was accepted")
	}
}

// TestFIoHashAfterHeaderChange: the digest covers the final payload, so a corrected field changes it.
func TestFIoHashAfterHeaderChange(t *testing.T) {
	if !FIoHashAvailable() {
		t.Skip("no BLAKE3 in this build")
	}
	good := NewEmptyContainerHeader(7).Serialize()
	bad := append([]byte{}, good...)
	binary.LittleEndian.PutUint64(bad[52:], 0)
	if FIoHash(good) == FIoHash(bad) {
		t.Fatal("the digest did not change with the serial info")
	}
}

// TestReferenceHeaderExactParsing pins the boundaries of the real 38-package header instead of trusting a round
// trip that could repeat the same mistake. The trailing flag in this file sits after the soft-reference window, so
// offset+size lands at 2492 while the flag occupies 2492..2496.
func TestReferenceHeaderExactParsing(t *testing.T) {
	root := projectRoot(t)
	toc := filepath.Join(root, ".local/reference-mods/ref-ru/atmosphere/contents/0100606026F12000/romfs/FFRS/Content/Paks/z_ffrs_ru_P.utoc")
	if _, err := os.Stat(toc); err != nil {
		t.Skip("reference container not present")
	}
	c, err := Open(toc)
	if err != nil {
		t.Fatal(err)
	}
	ucas, err := os.ReadFile(c.UcasPath)
	if err != nil {
		t.Fatal(err)
	}
	var payload []byte
	for _, e := range c.Entries() {
		if TypeOf(e.ID) != ChunkContainerHeader {
			continue
		}
		for _, b := range e.Blocks {
			payload = append(payload, ucas[b.Offset:b.Offset+int64(b.Compressed)]...)
		}
		payload = payload[:e.Length]
		break
	}
	if payload == nil {
		t.Fatal("no container header chunk")
	}
	h, err := ParseContainerHeader(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != 2496 {
		t.Fatalf("header is %d bytes, want 2496", len(payload))
	}
	if len(h.PackageIDs) != 38 || len(h.StoreEntries) != 0 || len(h.OptionalIDs) != 0 || len(h.OptionalStore) != 0 {
		t.Fatalf("arrays: %d packages, %d store, %d optional, %d optional store",
			len(h.PackageIDs), len(h.StoreEntries), len(h.OptionalIDs), len(h.OptionalStore))
	}
	if want := [12]byte{0xf1, 0x45, 0xdb, 0xc9, 0x0d, 0xee, 0xdc, 0x00, 0x7c, 0xc1, 0xb2, 0xa1}; PackageID(want) != h.PackageIDs[0] {
		t.Fatalf("first package id = %x, want %x", h.PackageIDs[0], want)
	}
	if h.SoftRefOffset != 2488 || h.SoftRefSize != 4 {
		t.Fatalf("serial info = %d+%d, want 2488+4", h.SoftRefOffset, h.SoftRefSize)
	}
	if !h.FlagAfterWindow {
		t.Fatal("reference header was not recognised as the trailing-flag variant")
	}
	// The middle starts right after the optional segment buffer at byte 488 and runs to the serial info at 2472.
	if len(h.Middle) != 1984 {
		t.Fatalf("middle region = %d bytes, want 1984", len(h.Middle))
	}
	consumed := 16 + 4 + 38*12 + 4 + 4 + 4 + len(h.Middle) + 16 + int(h.SoftRefSize) + 4
	if consumed != len(payload) {
		t.Fatalf("parser consumed %d of %d bytes", consumed, len(payload))
	}
	if h.HasSoftRefs {
		t.Fatal("soft references flag is set in the reference header")
	}
}
