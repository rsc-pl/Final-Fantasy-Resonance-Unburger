package build

// Tests for the build matrix. They run against a synthetic localisation resource in a temporary directory:
// nothing here is a claim about the shipped game, its container or its runtime.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"unburger/internal/corpuspack"
	"unburger/internal/locres"
	"unburger/internal/variant"
)

func fixtureCorpus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	jp1, en1 := "おはよう、レインさん。", "Good morning, Rain-san."
	real1 := "Morning, Rain-san."
	jp2, en2 := "買う", "Buy"
	f := corpuspack.File{
		SchemaVersion: corpuspack.SchemaVersion,
		Group:         corpuspack.Group{Type: "dialogue_line", Namespace: "ST_Test_01"},
		Records: []corpuspack.Record{
			{
				RecordID: "rec-1",
				Source:   corpuspack.Source{Set: "set-02", Namespace: "ST_Test_01", Key: "Test_001", JPSHA256: corpuspack.HashText(jp1), OfficialENSHA256: corpuspack.HashText(en1)},
				Text:     corpuspack.Text{JP: &jp1, OfficialEN: &en1, RealEN: &real1},
				Review:   corpuspack.Review{Status: corpuspack.StatusKeep, Reason: str("reviewed line")},
				Function: "dialogue_line",
			},
			{
				RecordID: "rec-2",
				Source:   corpuspack.Source{Set: "set-02", Namespace: "ST_UI_Shop_Word", Key: "Buy", JPSHA256: corpuspack.HashText(jp2), OfficialENSHA256: corpuspack.HashText(en2)},
				Text:     corpuspack.Text{JP: &jp2, OfficialEN: &en2},
				Review:   corpuspack.Review{Status: corpuspack.StatusUnreviewed},
				Function: "ui_label",
			},
		},
	}
	p := filepath.Join(dir, "dialogue", "unassigned", "ST_Test_01.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := jsonIndent(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := corpuspack.RefreshManifest(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func fixtureLocres(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "Game.locres")
	blob, err := locres.Write(locres.File{Version: 3, Entries: []locres.Entry{
		{Namespace: "ST_Test_01", Key: "Test_001", Text: "Good morning, Rain-san.", SourceHash: 7, NamespaceHash: 0x1000, KeyHash: 0x2001},
		{Namespace: "ST_UI_Shop_Word", Key: "Buy", Text: "Buy", SourceHash: 8, NamespaceHash: 0x1000, KeyHash: 0x3001},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestBuildAllProducesEveryReachableTarget: the default run builds the whole matrix, produces one artefact per
// reachable target, and reports the unreachable variants without stopping the canonical artefacts.
func TestBuildAllProducesEveryReachableTarget(t *testing.T) {
	corpus := fixtureCorpus(t)
	loc := fixtureLocres(t)
	work := t.TempDir()
	dist := t.TempDir()
	targets, err := RunAll(context.Background(), AllOptions{Version: "0.1.0", CorpusDir: corpus, LocresIn: loc, DistDir: dist, WorkDir: work}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 6 {
		t.Fatalf("targets = %d, want the full matrix of 6", len(targets))
	}
	pass, blocked, notReady := 0, 0, 0
	for _, tg := range targets {
		switch tg.State {
		case StatePass:
			pass++
			if tg.Artifact == "" {
				t.Fatalf("%s/%s/%s passed without an artefact", tg.Platform, tg.Layout, tg.Edition)
			}
		case StateBlocked:
			blocked++
		case StateNotReady:
			notReady++
		}
	}
	// Both Switch layouts and the PC layout produce a canonical artefact; the three no-honorifics targets are not
	// ready because the overlay holds no approved line. A produced artefact is structurally valid, not runtime
	// approved, and its metadata keeps the runtime state UNVERIFIED.
	if pass != 3 || notReady != 3 || blocked != 0 {
		t.Fatalf("pass/not-ready/blocked = %d/%d/%d, want 3/3/0", pass, notReady, blocked)
	}
	names := map[string]bool{}
	for _, tg := range targets {
		if tg.Artifact != "" {
			names[filepath.Base(tg.Artifact)] = true
		}
	}
	for _, want := range []string{"ffrs-en-switch-emulator-canonical-v0.1.0.zip", "ffrs-en-switch-atmosphere-canonical-v0.1.0.zip"} {
		if !names[want] {
			t.Fatalf("missing artefact %s in %v", want, names)
		}
	}
	// No empty or mislabelled archive may exist.
	entries, _ := os.ReadDir(dist)
	for _, e := range entries {
		if strings.Contains(e.Name(), "no-honorifics") {
			t.Fatalf("a no-honorifics archive was published without approved overrides: %s", e.Name())
		}
	}
}

// TestLayoutsDifferOnlyInTheMountRoot keeps the two ZIP shapes apart and checks that both carry the same
// resource and a metadata file that names the edition.
func TestLayoutsDifferOnlyInTheMountRoot(t *testing.T) {
	corpus := fixtureCorpus(t)
	loc := fixtureLocres(t)
	dist := t.TempDir()
	targets, err := RunAll(context.Background(), AllOptions{
		Version: "0.1.0", CorpusDir: corpus, LocresIn: loc, DistDir: dist, WorkDir: t.TempDir(),
		Filter: Filter{Platform: PlatformSwitch, Edition: EditionCanonical},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("filtered targets = %d, want 2", len(targets))
	}
	for _, tg := range targets {
		if tg.State != StatePass {
			t.Fatalf("%s: %s", tg.Layout, tg.Reason)
		}
		names := readZipNames(t, tg.Artifact)
		// The payload travels as a patch archive, so the archive must land in Content/Paks, not as a loose file.
		want := filepath.ToSlash(filepath.Join(modRoot(tg), filepath.FromSlash(PatchPakRelPath(PatchPakName("0.1.0")))))
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
			if filepath.IsAbs(n) || containsDotDot(n) {
				t.Fatalf("%s: unsafe path %q", tg.Artifact, n)
			}
		}
		if !found {
			t.Fatalf("%s: no %s, got %v", tg.Artifact, want, names)
		}
		if tg.Platform == PlatformPC {
			// A PC package unpacks straight into the game folder: three files and nothing else, with the release
			// metadata beside the archive.
			if len(names) != 3 {
				t.Fatalf("%s: %d entries, want exactly the three mod files: %v", tg.Artifact, len(names), names)
			}
			for _, n := range names {
				if filepath.ToSlash(filepath.Dir(n)) != "FFRS/Content/Paks" {
					t.Fatalf("%s: entry %q is outside FFRS/Content/Paks", tg.Artifact, n)
				}
			}
			sidecar := strings.TrimSuffix(tg.Artifact, ".zip") + ".mod-meta.json"
			if _, err := os.Stat(sidecar); err != nil {
				t.Fatalf("%s: release metadata missing beside the archive: %v", tg.Artifact, err)
			}
			continue
		}
		meta := false
		for _, n := range names {
			if n == "mod-meta.json" {
				meta = true
			}
		}
		if !meta {
			t.Fatalf("%s: no mod-meta.json", tg.Artifact)
		}
	}
}

// TestVariantBlockedUntilApproved: an empty or invalid overlay can never produce a no-honorifics artefact, and a
// valid overlay still does not publish one until the build is asked for it — the canon stays the default.
func TestVariantBlockedUntilApproved(t *testing.T) {
	corpus := fixtureCorpus(t)
	loc := fixtureLocres(t)
	dist := t.TempDir()
	work := t.TempDir()
	idx, err := corpuspack.LoadIndex(corpus)
	if err != nil {
		t.Fatal(err)
	}
	bad := variant.File{SchemaVersion: 1, Variant: variant.NoHonorifics, Base: "canonical", Overrides: []variant.Override{
		{RecordID: "rec-1", BaseRealENSHA256: "deadbeef", RealEN: "Good morning, Rain."},
	}}
	if _, err := variant.Validate(bad, idx); err == nil {
		t.Fatal("a stale canonical hash was accepted")
	}
	good := variant.File{SchemaVersion: 1, Variant: variant.NoHonorifics, Base: "canonical", Overrides: []variant.Override{
		{RecordID: "rec-1", BaseRealENSHA256: corpuspack.HashText("Morning, Rain-san."), RealEN: "Good morning, Rain."},
	}}
	if _, err := variant.Validate(good, idx); err != nil {
		t.Fatal(err)
	}
	targets, err := RunAll(context.Background(), AllOptions{
		Version: "0.1.0", CorpusDir: corpus, LocresIn: loc, DistDir: dist, WorkDir: work,
		Filter: Filter{Platform: PlatformSwitch, Edition: EditionNoHonorifics},
	}, &good)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(targets))
	}
	for _, tg := range targets {
		if tg.State != StatePass {
			t.Fatalf("%s: %s", tg.Layout, tg.Reason)
		}
	}
}

// TestPCProducesCanonicalAndNoHonorifics pins the PC half of the matrix: both editions are reachable without a
// platform-specific adapter, the variant starts from the canonical composition and changes only the override it
// names, and the sidecars report the edition and leave runtime UNVERIFIED.
func TestPCProducesCanonicalAndNoHonorifics(t *testing.T) {
	corpus := fixtureCorpus(t)
	loc := fixtureLocres(t)
	idx, err := corpuspack.LoadIndex(corpus)
	if err != nil {
		t.Fatal(err)
	}
	vf := &variant.File{SchemaVersion: variant.SchemaVersion, Variant: variant.NoHonorifics, Base: "canonical",
		Overrides: []variant.Override{{RecordID: "rec-1", BaseRealENSHA256: corpuspack.HashText("Morning, Rain-san."), RealEN: "Good morning, Rain."}}}
	if _, err := variant.Validate(*vf, idx); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	dist := t.TempDir()
	targets, err := RunAll(context.Background(), AllOptions{
		Version: "0.1.0", CorpusDir: corpus, LocresIn: loc, DistDir: dist, WorkDir: work,
		Filter: Filter{Platform: PlatformPC},
	}, vf)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("PC targets = %d, want canonical and no-honorifics", len(targets))
	}
	for _, tg := range targets {
		if tg.State != StatePass {
			t.Fatalf("%s: %s (%s)", tg.Edition, tg.State, tg.Reason)
		}
		if got, want := filepath.Base(tg.Artifact), "ffrs-en-pc-"+tg.Edition+"-v0.1.0.zip"; got != want {
			t.Fatalf("artifact = %q, want %q", got, want)
		}
		sidecar := strings.TrimSuffix(tg.Artifact, ".zip") + ".mod-meta.json"
		raw, err := os.ReadFile(sidecar)
		if err != nil {
			t.Fatal(err)
		}
		var meta map[string]interface{}
		if err := json.Unmarshal(raw, &meta); err != nil {
			t.Fatal(err)
		}
		if meta["platform"] != PlatformPC || meta["edition"] != tg.Edition || meta["runtime"] != RuntimeUnknown {
			t.Fatalf("%s sidecar = %v", tg.Edition, meta)
		}
	}
	canonical, err := locres.Read(filepath.Join(work, "canonical", "Game.locres"))
	if err != nil {
		t.Fatal(err)
	}
	variantOut, err := locres.Read(filepath.Join(work, "no-honorifics", "Game.locres"))
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical.Entries) != len(variantOut.Entries) {
		t.Fatalf("variant entry count = %d, want %d", len(variantOut.Entries), len(canonical.Entries))
	}
	for i := range canonical.Entries {
		a, b := canonical.Entries[i], variantOut.Entries[i]
		if a.Namespace != b.Namespace || a.Key != b.Key || a.SourceHash != b.SourceHash ||
			a.NamespaceHash != b.NamespaceHash || a.KeyHash != b.KeyHash {
			t.Fatalf("variant changed entry identity or hashes at %d: %+v vs %+v", i, a, b)
		}
	}
	if canonical.Entries[0].Text != "Morning, Rain-san." || variantOut.Entries[0].Text != "Good morning, Rain." {
		t.Fatalf("canonical/variant text = %q/%q", canonical.Entries[0].Text, variantOut.Entries[0].Text)
	}
	if canonical.Entries[1].Text != "Buy" || variantOut.Entries[1].Text != "Buy" {
		t.Fatalf("an untouched entry changed: canonical=%q variant=%q", canonical.Entries[1].Text, variantOut.Entries[1].Text)
	}
}

func readZipNames(t *testing.T, path string) []string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var out []string
	for _, f := range r.File {
		out = append(out, f.Name)
	}
	return out
}

func str(s string) *string { return &s }

func jsonIndent(v interface{}) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func containsDotDot(p string) bool {
	for i := 0; i+1 < len(p); i++ {
		if p[i] == '.' && p[i+1] == '.' {
			return true
		}
	}
	return false
}

// TestSwitchSetRefusesIncomplete: a Switch payload is only complete with .pak, .utoc and .ucas under one
// basename. The requirement comes from the working reference mod, not from the extension names.
func TestSwitchSetRefusesIncomplete(t *testing.T) {
	dir := t.TempDir()
	name := "z_test_P.pak"
	for _, f := range []string{"z_test_P.pak"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := SwitchSetComplete(dir, name); err == nil {
		t.Fatal("a payload with only a pak was accepted as complete")
	}
	for _, f := range []string{"z_test_P.utoc", "z_test_P.ucas"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := SwitchSetComplete(dir, name); err != nil {
		t.Fatalf("a complete set was refused: %v", err)
	}
}

// TestPackagedZipPaths opens the archives the build actually writes and checks every entry path, so the shape is
// verified where a user would see it rather than in an intermediate value.
func TestPackagedZipPaths(t *testing.T) {
	dir := t.TempDir()
	payload := filepath.Join(dir, "payload")
	if err := os.MkdirAll(payload, 0o755); err != nil {
		t.Fatal(err)
	}
	blob, err := locres.Write(locres.File{Version: 3, Entries: []locres.Entry{
		{Namespace: "ST_Test", Key: "Key", Text: "Text", SourceHash: 1, NamespaceHash: 0x11, KeyHash: 0x22},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "Game.locres"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(dir, "dist")
	cases := map[string]string{
		LayoutAtmosphere: filepath.ToSlash(filepath.Join("atmosphere", "contents", TitleID, "romfs", "FFRS", "Content", "Paks")),
		LayoutEmulator:   filepath.ToSlash(filepath.Join(ModFolder, "romfs", "FFRS", "Content", "Paks")),
	}
	for layout, wantDir := range cases {
		zpath, err := PackageLayout(payload, dist, Target{Platform: PlatformSwitch, Layout: layout, Edition: EditionCanonical,
			Ready: true, State: StatePass}, "0.1.0")
		if err != nil {
			t.Fatalf("%s: %v", layout, err)
		}
		zr, err := zip.OpenReader(zpath)
		if err != nil {
			t.Fatalf("%s: %v", layout, err)
		}
		seen := map[string]bool{}
		for _, f := range zr.File {
			seen[f.Name] = true
		}
		zr.Close()
		paks := 0
		for name := range seen {
			if strings.HasSuffix(name, ".pak") || strings.HasSuffix(name, ".utoc") || strings.HasSuffix(name, ".ucas") {
				paks++
			}
		}
		if paks < 3 {
			t.Fatalf("%s: expected the three binaries, saw %v", layout, keysOf(seen))
		}
		for _, ext := range []string{".pak", ".utoc", ".ucas"} {
			found := false
			for name := range seen {
				if strings.HasSuffix(name, ext) && filepath.ToSlash(filepath.Dir(name)) == wantDir {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s: no %s directly under %q (saw %v)", layout, ext, wantDir, keysOf(seen))
			}
		}
		for name := range seen {
			if strings.HasPrefix(name, "atmosphere/") && layout == LayoutEmulator {
				t.Fatalf("emulator archive still carries an atmosphere path: %s", name)
			}
			if strings.HasPrefix(name, ModFolder+"/") && layout == LayoutAtmosphere {
				t.Fatalf("atmosphere archive carries the mod folder: %s", name)
			}
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestPCPackageIsDropIn pins the PC shape: three files under FFRS/Content/Paks, no release metadata inside the
// archive, and the same bytes on a repeated build so a release is reproducible.
func TestPCPackageIsDropIn(t *testing.T) {
	dir := t.TempDir()
	payload := filepath.Join(dir, "payload")
	if err := os.MkdirAll(payload, 0o755); err != nil {
		t.Fatal(err)
	}
	blob, err := locres.Write(locres.File{Version: 3, Entries: []locres.Entry{
		{Namespace: "ST_Test", Key: "Key", Text: "Text", SourceHash: 1, NamespaceHash: 0x11, KeyHash: 0x22},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "Game.locres"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	tg := Target{Platform: PlatformPC, Layout: LayoutPC, Edition: EditionCanonical, Ready: true, State: StatePass}
	var first []byte
	for i := 0; i < 2; i++ {
		dist := filepath.Join(dir, fmt.Sprintf("dist%d", i))
		zpath, err := PackageLayout(payload, dist, tg, "0.1.0")
		if err != nil {
			t.Fatal(err)
		}
		if got, want := filepath.Base(zpath), "ffrs-en-pc-canonical-v0.1.0.zip"; got != want {
			t.Fatalf("artifact %q, want %q", got, want)
		}
		zr, err := zip.OpenReader(zpath)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		zr.Close()
		sort.Strings(names)
		want := []string{
			"FFRS/Content/Paks/z_ffrs_unburger_v0.1.0_P.pak",
			"FFRS/Content/Paks/z_ffrs_unburger_v0.1.0_P.ucas",
			"FFRS/Content/Paks/z_ffrs_unburger_v0.1.0_P.utoc",
		}
		if len(names) != len(want) {
			t.Fatalf("entries = %v, want exactly %v", names, want)
		}
		for i := range want {
			if names[i] != want[i] {
				t.Fatalf("entries = %v, want %v", names, want)
			}
		}
		sidecar := filepath.Join(dist, "ffrs-en-pc-canonical-v0.1.0.mod-meta.json")
		raw, err := os.ReadFile(sidecar)
		if err != nil {
			t.Fatalf("release metadata beside archive: %v", err)
		}
		if !strings.Contains(string(raw), `"platform": "pc"`) {
			t.Fatalf("sidecar does not describe the pc platform: %s", raw)
		}
		data, err := os.ReadFile(zpath)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = data
		} else if !bytes.Equal(first, data) {
			t.Fatal("two builds of the same payload produced different archives")
		}
	}
}
