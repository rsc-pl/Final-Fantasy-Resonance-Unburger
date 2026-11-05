package build

// The version reaches the archives, the metadata and the artefact name, and the corpus on disk is never touched.

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unburger/internal/corpuspack"
	"unburger/internal/locres"
	"unburger/internal/version"
)

// versionFixture builds a corpus with one approved line that carries the marker twice, and one English archive
// holding that record.
func versionFixture(t *testing.T) (corpus, locresPath string) {
	t.Helper()
	dir := t.TempDir()
	jp := "© SQUARE ENIX"
	off := "© SQUARE ENIX"
	real := "© SQUARE ENIX\r\nUNBURGER MOD v@@UNBURGER_VERSION@@\r\n@@UNBURGER_VERSION@@"
	f := corpuspack.File{
		SchemaVersion: corpuspack.SchemaVersion,
		Group:         corpuspack.Group{Type: "ui_label", Namespace: "ST_UI_TitleMenu"},
		Records: []corpuspack.Record{{
			RecordID: "rec-title",
			Source:   corpuspack.Source{Set: "set-02", Namespace: "ST_UI_TitleMenu", Key: "UI_Title_Copyright", JPSHA256: corpuspack.HashText(jp), OfficialENSHA256: corpuspack.HashText(off)},
			Text:     corpuspack.Text{JP: &jp, OfficialEN: &off, RealEN: &real},
			Review:   corpuspack.Review{Status: corpuspack.StatusChange, Reason: str("reviewed change")},
			Function: "ui_label",
		}},
	}
	p := filepath.Join(dir, "ui", "menu", "ST_UI_TitleMenu.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := corpuspack.RefreshManifest(dir); err != nil {
		t.Fatal(err)
	}
	lp := filepath.Join(t.TempDir(), "Game.locres")
	blob, err := locres.Write(locres.File{Version: 3, Entries: []locres.Entry{
		{Namespace: "ST_UI_TitleMenu", Key: "UI_Title_Copyright", Text: off, SourceHash: 1, NamespaceHash: 0x1000, KeyHash: 0x2001},
		{Namespace: "ST_UI_TitleMenu", Key: "UI_Title_Temp", Text: "Trial version", SourceHash: 2, NamespaceHash: 0x1000, KeyHash: 0x3001},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lp, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, lp
}

func fileHash(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestVersionReachesTheArchive: every marker becomes the version, and the corpus file is byte-identical after.
func TestVersionReachesTheArchive(t *testing.T) {
	corpus, lp := versionFixture(t)
	corpusFile := filepath.Join(corpus, "ui", "menu", "ST_UI_TitleMenu.json")
	before := fileHash(t, corpusFile)

	out := t.TempDir()
	rep, err := Run(context.Background(), Options{CorpusDir: corpus, LocresIn: lp, OutDir: out, Version: "2.5.0"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StatePass {
		t.Fatalf("state = %s: %s", rep.State, rep.Reason)
	}
	if rep.Markers != 2 {
		t.Fatalf("markers replaced = %d, want 2", rep.Markers)
	}
	back, err := locres.Read(rep.Output)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, e := range back.Entries {
		if e.Key == "UI_Title_Copyright" {
			got = e.Text
		}
	}
	if strings.Contains(got, version.Marker) {
		t.Fatalf("an unresolved marker reached the resource: %q", got)
	}
	if strings.Count(got, "2.5.0") != 2 {
		t.Fatalf("version occurrences = %d in %q", strings.Count(got, "2.5.0"), got)
	}
	if !strings.HasPrefix(got, "© SQUARE ENIX") {
		t.Fatalf("the copyright line was not preserved: %q", got)
	}
	if after := fileHash(t, corpusFile); after != before {
		t.Fatal("the build rewrote a corpus file")
	}
	// A different version produces a different resource, with no change to the corpus.
	if _, err := Run(context.Background(), Options{CorpusDir: corpus, LocresIn: lp, OutDir: t.TempDir(), Version: "2.5.1"}); err != nil {
		t.Fatal(err)
	}
	if fileHash(t, corpusFile) != before {
		t.Fatal("changing the version changed the corpus")
	}
}

// TestVersionFailClosed: a missing, malformed or empty version stops the build instead of shipping a resource
// with a raw marker in it.
func TestVersionFailClosed(t *testing.T) {
	corpus, lp := versionFixture(t)
	for _, v := range []string{"", "v1.0.0", "1.0", "latest"} {
		rep, err := Run(context.Background(), Options{CorpusDir: corpus, LocresIn: lp, OutDir: t.TempDir(), Version: v})
		if err != nil {
			t.Fatal(err)
		}
		if rep.State == StatePass {
			t.Fatalf("version %q passed", v)
		}
		if _, err := os.Stat(filepath.Join(rep.Output)); err == nil && rep.Output != "" {
			t.Fatalf("version %q wrote %s", v, rep.Output)
		}
	}
}

// TestVersionInEveryArtefact: Build All names and labels every artefact with the same number.
func TestVersionInEveryArtefact(t *testing.T) {
	corpus, lp := versionFixture(t)
	dist := t.TempDir()
	targets, err := RunAll(context.Background(), AllOptions{
		Version: "3.1.4", CorpusDir: corpus, LocresIn: lp, DistDir: dist, WorkDir: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, tg := range targets {
		if tg.State != StatePass {
			continue
		}
		seen++
		if !strings.Contains(filepath.Base(tg.Artifact), "v3.1.4") {
			t.Fatalf("artefact %s does not carry the version", filepath.Base(tg.Artifact))
		}
		r, err := zip.OpenReader(tg.Artifact)
		if err != nil {
			t.Fatal(err)
		}
		var metaVersion string
		if tg.Platform == PlatformPC {
			sidecar := strings.TrimSuffix(tg.Artifact, ".zip") + ".mod-meta.json"
			b, err := os.ReadFile(sidecar)
			if err != nil {
				r.Close()
				t.Fatal(err)
			}
			var meta map[string]interface{}
			if err := json.Unmarshal(b, &meta); err != nil {
				r.Close()
				t.Fatal(err)
			}
			metaVersion, _ = meta["version"].(string)
			r.Close()
			if metaVersion != "3.1.4" {
				t.Fatalf("sidecar version = %q", metaVersion)
			}
			continue
		}
		for _, f := range r.File {
			if f.Name == "mod-meta.json" {
				rc, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				b, _ := io.ReadAll(rc)
				rc.Close()
				var meta map[string]interface{}
				if err := json.Unmarshal(b, &meta); err != nil {
					t.Fatal(err)
				}
				metaVersion, _ = meta["version"].(string)
			}
			if strings.HasSuffix(f.Name, ".locres") {
				rc, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				b, _ := io.ReadAll(rc)
				rc.Close()
				if strings.Contains(string(b), version.Marker) {
					t.Fatalf("%s ships a raw version marker", f.Name)
				}
			}
		}
		r.Close()
		if metaVersion != "3.1.4" {
			t.Fatalf("metadata version = %q, want 3.1.4", metaVersion)
		}
	}
	if seen == 0 {
		t.Fatal("no artefact was produced, so the check proved nothing")
	}
}
