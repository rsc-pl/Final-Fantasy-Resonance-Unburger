package pak

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPatchArchiveKeepsEveryOtherEntry: one entry is replaced, every other blob stays byte identical, and the
// archive still parses with all hashes intact.
func TestPatchArchiveKeepsEveryOtherEntry(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "FFRS-Switch.pak")
	old := bytes.Repeat([]byte("OLD-LOCRES"), 64)
	if _, err := WritePatch(src, PatchOptions{MountPoint: DefaultMountPoint, Entries: []PatchEntry{
		{Path: "Engine/Config/Base.ini", Data: []byte("[Core]\n")},
		{Path: VirtualPath, Data: old},
		{Path: "FFRS/Content/Foo.uasset", Data: []byte("asset bytes")},
	}}); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "patched.pak")
	payload := bytes.Repeat([]byte("NEW-LOCRES!"), 100)
	res, err := PatchArchive(InPlaceRequest{Src: src, Dst: dst, VirtualPath: VirtualPath, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if res.Entries != 3 || res.Copied != 2 {
		t.Fatalf("entries=%d copied=%d", res.Entries, res.Copied)
	}
	if res.TargetNew <= 0 || res.TargetOld <= 0 {
		t.Fatalf("target sizes %d -> %d", res.TargetOld, res.TargetNew)
	}
	got, err := ReadV11File(dst, VirtualPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("replacement does not read back")
	}
	for path, want := range map[string]string{"Engine/Config/Base.ini": "[Core]\n", "FFRS/Content/Foo.uasset": "asset bytes"} {
		b, err := ReadV11File(dst, path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if string(b) != want {
			t.Fatalf("%s changed to %q", path, string(b))
		}
	}
	idx, err := ReadV11(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Problems) > 0 {
		t.Fatalf("problems: %v", idx.Problems)
	}
	if idx.PathHashMatch != 3 {
		t.Fatalf("path hash matches = %d", idx.PathHashMatch)
	}
}

// TestPatchArchiveRefusesUnknownSource: an unrecognised or already modified archive is rejected before anything
// is written.
func TestPatchArchiveRefusesUnknownSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "FFRS-Switch.pak")
	if _, err := WritePatch(src, PatchOptions{MountPoint: DefaultMountPoint, Entries: []PatchEntry{
		{Path: VirtualPath, Data: []byte("orig")},
	}}); err != nil {
		t.Fatal(err)
	}
	_, err := PatchArchive(InPlaceRequest{
		Src: src, Dst: filepath.Join(dir, "out.pak"), VirtualPath: VirtualPath,
		Payload: []byte("new"), KnownSHA256: []string{strings.Repeat("0", 64)},
	})
	if err == nil {
		t.Fatal("an unknown source digest was accepted")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "out.pak")); statErr == nil {
		t.Fatal("a rejected source still produced an output file")
	}
}

// TestPatchArchiveRefusesMissingPath: a virtual path that the source does not contain is refused.
func TestPatchArchiveRefusesMissingPath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "FFRS-Switch.pak")
	if _, err := WritePatch(src, PatchOptions{MountPoint: DefaultMountPoint, Entries: []PatchEntry{
		{Path: "FFRS/Content/Other.locres", Data: []byte("x")},
	}}); err != nil {
		t.Fatal(err)
	}
	_, err := PatchArchive(InPlaceRequest{
		Src: src, Dst: filepath.Join(dir, "out.pak"), VirtualPath: VirtualPath, Payload: []byte("y"),
	})
	if err == nil {
		t.Fatal("a missing virtual path was accepted")
	}
}

// TestPatchArchiveInPlaceWritesBackup: an in-place write requires and produces a backup.
func TestPatchArchiveInPlaceWritesBackup(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "FFRS-Switch.pak")
	if _, err := WritePatch(src, PatchOptions{MountPoint: DefaultMountPoint, Entries: []PatchEntry{
		{Path: VirtualPath, Data: []byte("original")},
	}}); err != nil {
		t.Fatal(err)
	}
	before, _, err := FileSHA256(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PatchArchive(InPlaceRequest{Src: src, Dst: src, VirtualPath: VirtualPath, Payload: []byte("replaced")}); err == nil {
		t.Fatal("an in-place write without a backup path was accepted")
	}
	res, err := PatchArchive(InPlaceRequest{
		Src: src, Dst: src, Backup: filepath.Join(dir, "FFRS-Switch.pak.bak"),
		VirtualPath: VirtualPath, Payload: []byte("replaced"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.SrcSHA256 != before {
		t.Fatal("the source digest changed before the write")
	}
	bak, _, err := FileSHA256(filepath.Join(dir, "FFRS-Switch.pak.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if bak != before {
		t.Fatal("the backup does not match the original")
	}
	got, err := ReadV11File(src, VirtualPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "replaced" {
		t.Fatalf("in-place write produced %q", string(got))
	}
}
