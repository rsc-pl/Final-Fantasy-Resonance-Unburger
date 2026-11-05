package pak

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPatchRoundtrip: a written archive parses back through the independent reader, with every SHA-1 gate intact
// and the payload identical.
func TestPatchRoundtrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "FFRS-Unburger_P.pak")
	payload := []byte("\x0e\x14\x74\x75payload bytes of a localisation archive")
	locale := payload
	res, err := WritePatch(p, PatchOptions{
		MountPoint:   DefaultMountPoint,
		PathHashSeed: 0x9c4dd25a,
		Entries:      []PatchEntry{{Path: "FFRS/Content/Localization/Game/en/Game.locres", Data: locale}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.NumEntries != 1 {
		t.Fatalf("entries = %d", res.NumEntries)
	}
	idx, err := ReadV11(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Problems) > 0 {
		t.Fatalf("reader problems: %v", idx.Problems)
	}
	if idx.MountPoint != DefaultMountPoint {
		t.Fatalf("mount = %q", idx.MountPoint)
	}
	if idx.NumEntries != 1 || len(idx.Files) != 1 {
		t.Fatalf("entries = %d/%d", idx.NumEntries, len(idx.Files))
	}
	if idx.Files[0].Path != "FFRS/Content/Localization/Game/en/Game.locres" {
		t.Fatalf("path = %q", idx.Files[0].Path)
	}
	if idx.PathHashMatch != 1 {
		t.Fatalf("path hash matches = %d", idx.PathHashMatch)
	}
	if idx.FooterIndexSHA1 != idx.PrimarySHA1 {
		t.Fatal("footer index sha1 does not match the primary index")
	}
	if idx.PathHashSHA1 == "" || idx.FullDirSHA1 == "" {
		t.Fatal("secondary indexes were not hashed")
	}
	back, err := ReadV11File(p, idx.Files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, locale) {
		t.Fatal("payload changed in the roundtrip")
	}
	if res.Entries[0].SHA1 == "" {
		t.Fatal("no payload sha1 reported")
	}
	if idx.Files[0].SHA1 != res.Entries[0].SHA1 {
		t.Fatalf("reader sha1 %q != writer sha1 %q", idx.Files[0].SHA1, res.Entries[0].SHA1)
	}
}

// TestPatchDeterministic: the same input produces the same bytes.
func TestPatchDeterministic(t *testing.T) {
	dir := t.TempDir()
	opt := PatchOptions{MountPoint: DefaultMountPoint, Entries: []PatchEntry{
		{Path: "FFRS/Content/Localization/Game/en/Game.locres", Data: []byte("same bytes")},
	}}
	a := filepath.Join(dir, "a.pak")
	b := filepath.Join(dir, "b.pak")
	if _, err := WritePatch(a, opt); err != nil {
		t.Fatal(err)
	}
	if _, err := WritePatch(b, opt); err != nil {
		t.Fatal(err)
	}
	ba, _ := os.ReadFile(a)
	bb, _ := os.ReadFile(b)
	if !bytes.Equal(ba, bb) {
		t.Fatal("two writes of the same input differ")
	}
}

// TestPatchRejectsUnsupported: a writer that cannot represent a case says so instead of emitting a broken file.
func TestPatchRejectsUnsupported(t *testing.T) {
	dir := t.TempDir()
	cases := []PatchOptions{
		{MountPoint: "", Entries: []PatchEntry{{Path: "a", Data: []byte("x")}}},
		{MountPoint: DefaultMountPoint},
		{MountPoint: DefaultMountPoint, Entries: []PatchEntry{{Path: "../escape", Data: []byte("x")}}},
		{MountPoint: DefaultMountPoint, Entries: []PatchEntry{{Path: "a", Data: []byte("x")}, {Path: "a", Data: []byte("y")}}},
		{MountPoint: DefaultMountPoint, Entries: []PatchEntry{{Path: "a", Data: nil}}},
	}
	for i, c := range cases {
		if _, err := WritePatch(filepath.Join(dir, "bad.pak"), c); err == nil {
			t.Fatalf("case %d was accepted", i)
		}
	}
}

// TestRealArchiveIndex: the reader parses the shipped archive's own index, which is the ground truth for the
// mount point, the virtual path and the hash recipe. Skipped when the private archive is absent.
func TestRealArchiveIndex(t *testing.T) {
	root := projectRoot(t)
	p := filepath.Join(root, ".local/originals/switch/romfs/FFRS/Content/Paks/FFRS-Switch.pak")
	if _, err := os.Stat(p); err != nil {
		t.Skip("private archive not present")
	}
	idx, err := ReadV11(p)
	if err != nil {
		t.Fatal(err)
	}
	if idx.MountPoint != DefaultMountPoint {
		t.Fatalf("mount = %q", idx.MountPoint)
	}
	if idx.NumEntries != 2363 {
		t.Fatalf("entries = %d, want 2363", idx.NumEntries)
	}
	if len(idx.Problems) > 0 {
		t.Fatalf("problems: %d, first %s", len(idx.Problems), idx.Problems[0])
	}
	if idx.PathHashMatch != int(idx.NumEntries) {
		t.Fatalf("path hash matches = %d of %d", idx.PathHashMatch, idx.NumEntries)
	}
	if idx.FooterIndexSHA1 != idx.PrimarySHA1 {
		t.Fatal("footer index sha1 does not match")
	}
	found := false
	for _, f := range idx.Files {
		if f.Path == "FFRS/Content/Localization/Game/en/Game.locres" {
			found = true
			if f.Method == 0 {
				t.Fatal("expected the shipped resource to be compressed")
			}
		}
	}
	if !found {
		t.Fatal("the shipped index does not name the english game resource")
	}
	if !strings.HasPrefix(idx.Files[0].Path, "Engine/") && !strings.HasPrefix(idx.Files[0].Path, "FFRS/") {
		t.Fatalf("unexpected first path %q", idx.Files[0].Path)
	}
}
