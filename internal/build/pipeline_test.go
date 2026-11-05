package build

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unburger/internal/variant"

	"unburger/internal/corpuspack"
	"unburger/internal/locres"
)

// TestBuildBlockedWithoutResource keeps the honest boundary: no authentic localisation resource means BLOCKED
// with a reason, not a silent success and not a fabricated payload.
func TestBuildBlockedWithoutResource(t *testing.T) {
	rep, err := Run(context.Background(), Options{
		CorpusDir: t.TempDir(),
		LocresIn:  filepath.Join(t.TempDir(), "absent.locres"),
		OutDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateBlocked || rep.Reason == "" {
		t.Fatalf("state = %q reason = %q, want BLOCKED with a reason", rep.State, rep.Reason)
	}
}

// TestBuildComposesAndVerifies runs the whole path on a small corpus and a resource written by our own writer:
// a record with a real translation takes it, everything else keeps the official text, and the result parses
// back to exactly what was composed.
func TestBuildComposesAndVerifies(t *testing.T) {
	dir := t.TempDir()
	corpus := filepath.Join(dir, "translations")
	jp := "買う"
	official := "Buy"
	real := "Purchase"
	src := corpuspack.File{
		SchemaVersion: corpuspack.SchemaVersion,
		Group:         corpuspack.Group{Type: "ui_label", Namespace: "ST_UI_Shop_Word"},
		Records: []corpuspack.Record{
			{
				RecordID: "rec-1",
				Source:   corpuspack.Source{Set: "set-02", Namespace: "ST_UI_Shop_Word", Key: "Buy", JPSHA256: corpuspack.HashText(jp), OfficialENSHA256: corpuspack.HashText(official)},
				Text:     corpuspack.Text{JP: &jp, OfficialEN: &official, RealEN: &real},
				Review:   corpuspack.Review{Status: corpuspack.StatusChange, Reason: strptr("approved")},
				Function: "ui_label",
			},
			{
				RecordID: "rec-2",
				Source:   corpuspack.Source{Set: "set-02", Namespace: "ST_UI_Shop_Word", Key: "Sell", JPSHA256: corpuspack.HashText("売る"), OfficialENSHA256: corpuspack.HashText("Sell")},
				Text:     corpuspack.Text{JP: strptr("売る"), OfficialEN: strptr("Sell")},
				Review:   corpuspack.Review{Status: corpuspack.StatusUnreviewed},
				Function: "ui_label",
			},
		},
	}
	p := filepath.Join(corpus, "ui", "shop", "ST_UI_Shop_Word.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, _ := jsonMarshal(src)
	if err := os.WriteFile(p, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := corpuspack.RefreshManifest(corpus); err != nil {
		t.Fatal(err)
	}

	resource := filepath.Join(dir, "Game.locres")
	srcLoc := locres.File{Version: 3, Entries: []locres.Entry{
		{Namespace: "ST_UI_Shop_Word", Key: "Buy", Text: "Buy", SourceHash: 1, NamespaceHash: 0x1000, KeyHash: 0x2001},
		{Namespace: "ST_UI_Shop_Word", Key: "Sell", Text: "Sell", SourceHash: 2, NamespaceHash: 0x1000, KeyHash: 0x3001},
		{Namespace: "ST_Unknown", Key: "Orphan", Text: "Orphan", SourceHash: 3, NamespaceHash: 0x1000, KeyHash: 0x3001},
	}}
	lb, err := locres.Write(srcLoc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resource, lb, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := Run(context.Background(), Options{CorpusDir: corpus, LocresIn: resource, OutDir: filepath.Join(dir, "out"), Version: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StatePass {
		t.Fatalf("state = %s: %s", rep.State, rep.Reason)
	}
	if rep.RealEN != 1 || rep.OfficialEN != 1 || rep.MissingInCorpus != 1 || rep.TextDrift != 0 {
		t.Fatalf("counts = %+v", rep)
	}
	if rep.Runtime != RuntimeUnknown {
		t.Fatalf("runtime reported as %q without a game test", rep.Runtime)
	}
	back, err := locres.Read(rep.Output)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range back.Entries {
		if e.Namespace == "ST_UI_Shop_Word" && e.Key == "Buy" && e.Text != "Purchase" {
			t.Fatalf("real translation not applied: %q", e.Text)
		}
		if e.Namespace == "ST_UI_Shop_Word" && e.Key == "Sell" && e.Text != "Sell" {
			t.Fatalf("fallback changed the official text: %q", e.Text)
		}
	}
	if back.Entries[0].SourceHash != 1 {
		t.Fatal("the source hash of the resource was not preserved")
	}
}

// TestPartialRealTLKeepsCompleteResource is the fallback contract: a partial real translation changes only its
// own entries, every other entry keeps the original English text, an empty real value is never substituted, and
// the entry count, order, namespaces, keys and source hashes stay identical.
func TestPartialRealTLKeepsCompleteResource(t *testing.T) {
	dir := t.TempDir()
	corpus := filepath.Join(dir, "translations")
	jpA, offA, realA := "買う", "Buy", "Purchase"
	jpB, offB := "売る", "Sell"
	jpC, offC, emptyC := "保つ", "Keep", ""
	jpD, offD := "捨てる", "Drop"
	src := corpuspack.File{
		SchemaVersion: corpuspack.SchemaVersion,
		Group:         corpuspack.Group{Type: "ui_label", Namespace: "ST_UI_Shop_Word"},
		Records: []corpuspack.Record{
			{RecordID: "a", Source: corpuspack.Source{Set: "s", Namespace: "ST_UI_Shop_Word", Key: "A", JPSHA256: corpuspack.HashText(jpA), OfficialENSHA256: corpuspack.HashText(offA)}, Text: corpuspack.Text{JP: &jpA, OfficialEN: &offA, RealEN: &realA}, Review: corpuspack.Review{Status: corpuspack.StatusChange}, Function: "ui_label"},
			{RecordID: "b", Source: corpuspack.Source{Set: "s", Namespace: "ST_UI_Shop_Word", Key: "B", JPSHA256: corpuspack.HashText(jpB), OfficialENSHA256: corpuspack.HashText(offB)}, Text: corpuspack.Text{JP: &jpB, OfficialEN: &offB}, Review: corpuspack.Review{Status: corpuspack.StatusUnreviewed}, Function: "ui_label"},
			{RecordID: "c", Source: corpuspack.Source{Set: "s", Namespace: "ST_UI_Shop_Word", Key: "C", JPSHA256: corpuspack.HashText(jpC), OfficialENSHA256: corpuspack.HashText(offC)}, Text: corpuspack.Text{JP: &jpC, OfficialEN: &offC, RealEN: &emptyC}, Review: corpuspack.Review{Status: corpuspack.StatusUnreviewed}, Function: "ui_label"},
			{RecordID: "d", Source: corpuspack.Source{Set: "s", Namespace: "ST_UI_Shop_Word", Key: "D", JPSHA256: corpuspack.HashText(jpD), OfficialENSHA256: corpuspack.HashText(offD)}, Text: corpuspack.Text{JP: &jpD, OfficialEN: &offD}, Review: corpuspack.Review{Status: corpuspack.StatusUnreviewed}, Function: "ui_label"},
		},
	}
	p := filepath.Join(corpus, "ui", "ST_UI_Shop_Word.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(src)
	if err := os.WriteFile(p, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := corpuspack.RefreshManifest(corpus); err != nil {
		t.Fatal(err)
	}
	resource := filepath.Join(dir, "Game.locres")
	in := locres.File{Version: 3, Entries: []locres.Entry{
		{Namespace: "ST_UI_Shop_Word", Key: "A", Text: "Buy", SourceHash: 1, NamespaceHash: 11, KeyHash: 21},
		{Namespace: "ST_UI_Shop_Word", Key: "B", Text: "Sell", SourceHash: 2, NamespaceHash: 11, KeyHash: 22},
		{Namespace: "ST_UI_Shop_Word", Key: "C", Text: "Keep", SourceHash: 3, NamespaceHash: 11, KeyHash: 23},
		{Namespace: "ST_UI_Shop_Word", Key: "D", Text: "Drop", SourceHash: 4, NamespaceHash: 11, KeyHash: 24},
	}}
	lb, err := locres.Write(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resource, lb, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), Options{CorpusDir: corpus, LocresIn: resource, OutDir: filepath.Join(dir, "out"), Version: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StatePass || rep.Runtime != RuntimeUnknown {
		t.Fatalf("state/runtime = %s/%s", rep.State, rep.Runtime)
	}
	if rep.Entries != 4 || rep.RealEN != 1 || rep.OfficialEN != 3 || rep.MissingInCorpus != 0 {
		t.Fatalf("counts = %+v", rep)
	}
	back, err := locres.Read(rep.Output)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Entries) != len(in.Entries) {
		t.Fatalf("entry count changed: %d != %d", len(back.Entries), len(in.Entries))
	}
	want := []string{"Purchase", "Sell", "Keep", "Drop"}
	for i, e := range back.Entries {
		if e.Key != in.Entries[i].Key || e.Namespace != in.Entries[i].Namespace {
			t.Fatalf("entry %d identity changed: %s/%s", i, e.Namespace, e.Key)
		}
		if e.Text != want[i] {
			t.Fatalf("entry %d text = %q, want %q", i, e.Text, want[i])
		}
		if e.SourceHash != in.Entries[i].SourceHash || e.NamespaceHash != in.Entries[i].NamespaceHash || e.KeyHash != in.Entries[i].KeyHash {
			t.Fatalf("entry %d hashes changed", i)
		}
	}
}

// TestDirectoryCompositionKeepsPartialFallback exercises the directory input the CLI and TUI actually pass: the
// English archive is rewritten in place and everything else stays byte-identical. A partial RealTL changes only
// its own entries; unreviewed, empty and unknown lines keep the official English from the authentic resource, and
// entry identity, order and hashes do not move. The unchanged Japanese archive is not emitted, and packaging the
// result keeps the neutral metadata.
func TestDirectoryCompositionKeepsPartialFallback(t *testing.T) {
	dir := t.TempDir()
	corpus := filepath.Join(dir, "translations")
	jpA, offA, realA := "買う", "Buy", "Purchase"
	jpB, offB := "売る", "Sell"
	jpC, offC, emptyC := "保つ", "Keep", ""
	jpD := "捨てる"
	if err := os.MkdirAll(corpus, 0o755); err != nil {
		t.Fatal(err)
	}
	src := corpuspack.File{
		SchemaVersion: corpuspack.SchemaVersion,
		Group:         corpuspack.Group{Type: "ui_label", Namespace: "ST_UI_Shop_Word"},
		Records: []corpuspack.Record{
			{RecordID: "a", Source: corpuspack.Source{Set: "s", Namespace: "ST_UI_Shop_Word", Key: "A", JPSHA256: corpuspack.HashText(jpA), OfficialENSHA256: corpuspack.HashText(offA)}, Text: corpuspack.Text{JP: &jpA, OfficialEN: &offA, RealEN: &realA}, Review: corpuspack.Review{Status: corpuspack.StatusChange}, Function: "ui_label"},
			{RecordID: "b", Source: corpuspack.Source{Set: "s", Namespace: "ST_UI_Shop_Word", Key: "B", JPSHA256: corpuspack.HashText(jpB), OfficialENSHA256: corpuspack.HashText(offB)}, Text: corpuspack.Text{JP: &jpB, OfficialEN: &offB}, Review: corpuspack.Review{Status: corpuspack.StatusUnreviewed}, Function: "ui_label"},
			{RecordID: "c", Source: corpuspack.Source{Set: "s", Namespace: "ST_UI_Shop_Word", Key: "C", JPSHA256: corpuspack.HashText(jpC), OfficialENSHA256: corpuspack.HashText(offC)}, Text: corpuspack.Text{JP: &jpC, OfficialEN: &offC, RealEN: &emptyC}, Review: corpuspack.Review{Status: corpuspack.StatusUnreviewed}, Function: "ui_label"},
		},
	}
	p := filepath.Join(corpus, "ui", "ST_UI_Shop_Word.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(src)
	if err := os.WriteFile(p, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := corpuspack.RefreshManifest(corpus); err != nil {
		t.Fatal(err)
	}
	locresDir := filepath.Join(dir, "locres")
	if err := os.MkdirAll(locresDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeArchive := func(name string, entries []locres.Entry) {
		t.Helper()
		b, err := locres.Write(locres.File{Version: 3, Entries: entries})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(locresDir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	english := []locres.Entry{
		{Namespace: "ST_UI_Shop_Word", Key: "A", Text: offA, SourceHash: 1, NamespaceHash: 11, KeyHash: 21},
		{Namespace: "ST_UI_Shop_Word", Key: "B", Text: offB, SourceHash: 2, NamespaceHash: 11, KeyHash: 22},
		{Namespace: "ST_UI_Shop_Word", Key: "C", Text: offC, SourceHash: 3, NamespaceHash: 11, KeyHash: 23},
		{Namespace: "ST_Unknown", Key: "D", Text: "Drop", SourceHash: 4, NamespaceHash: 12, KeyHash: 24},
	}
	writeArchive("English.locres", english)
	writeArchive("Japanese.locres", []locres.Entry{
		{Namespace: "ST_UI_Shop_Word", Key: "A", Text: jpA, SourceHash: 1, NamespaceHash: 11, KeyHash: 21},
		{Namespace: "ST_UI_Shop_Word", Key: "B", Text: jpB, SourceHash: 2, NamespaceHash: 11, KeyHash: 22},
		{Namespace: "ST_UI_Shop_Word", Key: "C", Text: jpC, SourceHash: 3, NamespaceHash: 11, KeyHash: 23},
		{Namespace: "ST_Unknown", Key: "D", Text: jpD, SourceHash: 4, NamespaceHash: 12, KeyHash: 24},
	})
	outDir := filepath.Join(dir, "out")
	rep, err := Run(context.Background(), Options{CorpusDir: corpus, LocresIn: locresDir, OutDir: outDir, Version: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StatePass || rep.Runtime != RuntimeUnknown || rep.Entries != 4 || rep.RealEN != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(outDir, "Japanese.locres")); err == nil {
		t.Fatal("an unchanged archive was written to the payload")
	}
	back, err := locres.Read(filepath.Join(outDir, "English.locres"))
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Entries) != len(english) {
		t.Fatalf("entry count = %d, want %d", len(back.Entries), len(english))
	}
	want := []string{realA, offB, offC, "Drop"}
	for i, e := range back.Entries {
		if e.Namespace != english[i].Namespace || e.Key != english[i].Key {
			t.Fatalf("entry %d identity changed: %s/%s", i, e.Namespace, e.Key)
		}
		if e.SourceHash != english[i].SourceHash || e.NamespaceHash != english[i].NamespaceHash || e.KeyHash != english[i].KeyHash {
			t.Fatalf("entry %d hashes changed", i)
		}
		if e.Text != want[i] {
			t.Fatalf("entry %d text = %q, want %q", i, e.Text, want[i])
		}
		if e.Text == "" {
			t.Fatalf("entry %d became empty", i)
		}
	}
	dist := filepath.Join(dir, "dist")
	zipPath, err := PackageLayout(outDir, dist, Target{Platform: PlatformPC, Layout: LayoutPC, Edition: EditionCanonical, Ready: true, State: StatePass}, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(strings.TrimSuffix(zipPath, ".zip") + ".mod-meta.json")
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["platform"] != PlatformPC || meta["edition"] != EditionCanonical || meta["runtime"] != RuntimeUnknown || meta["resource"] != LocresVirtualPath {
		t.Fatalf("metadata = %v", meta)
	}
}

// TestNoRealTLReportsNoPatchNeeded keeps an unchanged resource honest: no differing real translation means no
// patch is produced, and the reason says so instead of publishing a byte-identical file as a change.
func TestNoRealTLReportsNoPatchNeeded(t *testing.T) {
	dir := t.TempDir()
	corpus := filepath.Join(dir, "translations")
	jp, off := "買う", "Buy"
	src := corpuspack.File{
		SchemaVersion: corpuspack.SchemaVersion,
		Group:         corpuspack.Group{Type: "ui_label", Namespace: "ST_UI_Shop_Word"},
		Records:       []corpuspack.Record{{RecordID: "a", Source: corpuspack.Source{Set: "s", Namespace: "ST_UI_Shop_Word", Key: "A", JPSHA256: corpuspack.HashText(jp), OfficialENSHA256: corpuspack.HashText(off)}, Text: corpuspack.Text{JP: &jp, OfficialEN: &off}, Review: corpuspack.Review{Status: corpuspack.StatusUnreviewed}, Function: "ui_label"}},
	}
	p := filepath.Join(corpus, "ui", "ST_UI_Shop_Word.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(src)
	if err := os.WriteFile(p, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := corpuspack.RefreshManifest(corpus); err != nil {
		t.Fatal(err)
	}
	resource := filepath.Join(dir, "Game.locres")
	lb, _ := locres.Write(locres.File{Version: 3, Entries: []locres.Entry{{Namespace: "ST_UI_Shop_Word", Key: "A", Text: "Buy", SourceHash: 1, NamespaceHash: 11, KeyHash: 21}}})
	if err := os.WriteFile(resource, lb, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	rep, err := Run(context.Background(), Options{CorpusDir: corpus, LocresIn: resource, OutDir: out, Version: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateNotReady || rep.Reason == "" || rep.Output != "" {
		t.Fatalf("state/reason/output = %s/%q/%q", rep.State, rep.Reason, rep.Output)
	}
	if entries, err := os.ReadDir(out); err == nil && len(entries) != 0 {
		t.Fatalf("an unchanged resource produced %d output files", len(entries))
	}
}

func strptr(s string) *string { return &s }

func jsonMarshal(v interface{}) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// TestRunAllBuildsBothEditions is the path the TUI takes: it loads a variant, validates it against the index and
// hands it to RunAll. The canonical edition keeps the real translation, the variant edition replaces only the
// record the override names, and every other entry stays byte-identical.
func TestRunAllBuildsBothEditions(t *testing.T) {
	dir := t.TempDir()
	corpus := filepath.Join(dir, "translations")
	jp, official, real := "買う", "Buy", "Purchase"
	variantJP, variantOfficial, variantReal := "売る", "Sell", "Sell it"
	src := corpuspack.File{
		SchemaVersion: corpuspack.SchemaVersion,
		Group:         corpuspack.Group{Type: "ui_label", Namespace: "ST_UI_Shop_Word"},
		Records: []corpuspack.Record{
			{RecordID: "rec-1", Source: corpuspack.Source{Set: "set-02", Namespace: "ST_UI_Shop_Word", Key: "Buy", JPSHA256: corpuspack.HashText(jp), OfficialENSHA256: corpuspack.HashText(official)},
				Text: corpuspack.Text{JP: &jp, OfficialEN: &official, RealEN: &real}, Review: corpuspack.Review{Status: corpuspack.StatusChange, Reason: strptr("reviewed")}, Function: "ui_label"},
			{RecordID: "rec-2", Source: corpuspack.Source{Set: "set-02", Namespace: "ST_UI_Shop_Word", Key: "Sell", JPSHA256: corpuspack.HashText(variantJP), OfficialENSHA256: corpuspack.HashText(variantOfficial)},
				Text: corpuspack.Text{JP: &variantJP, OfficialEN: &variantOfficial, RealEN: &variantReal}, Review: corpuspack.Review{Status: corpuspack.StatusChange, Reason: strptr("reviewed")}, Function: "ui_label"},
			{RecordID: "rec-3", Source: corpuspack.Source{Set: "set-02", Namespace: "ST_UI_Shop_Word", Key: "Keep", JPSHA256: corpuspack.HashText("保つ"), OfficialENSHA256: corpuspack.HashText("Keep")},
				Text: corpuspack.Text{JP: strptr("保つ"), OfficialEN: strptr("Keep")}, Review: corpuspack.Review{Status: corpuspack.StatusKeep}, Function: "ui_label"},
		},
	}
	p := filepath.Join(corpus, "ui", "shop", "ST_UI_Shop_Word.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(src)
	if err := os.WriteFile(p, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := corpuspack.RefreshManifest(corpus); err != nil {
		t.Fatal(err)
	}
	resource := filepath.Join(dir, "Game.locres")
	in := locres.File{Version: 3, Entries: []locres.Entry{
		{Namespace: "ST_UI_Shop_Word", Key: "Buy", Text: official, SourceHash: 1, NamespaceHash: 0x11, KeyHash: 0x21},
		{Namespace: "ST_UI_Shop_Word", Key: "Sell", Text: variantOfficial, SourceHash: 2, NamespaceHash: 0x11, KeyHash: 0x22},
		{Namespace: "ST_UI_Shop_Word", Key: "Keep", Text: "Keep", SourceHash: 3, NamespaceHash: 0x11, KeyHash: 0x23},
	}}
	lb, err := locres.Write(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resource, lb, 0o644); err != nil {
		t.Fatal(err)
	}
	vf := &variant.File{
		SchemaVersion: variant.SchemaVersion,
		Variant:       variant.NoHonorifics,
		Base:          "canonical",
		Overrides: []variant.Override{{RecordID: "rec-2",
			BaseRealENSHA256: corpuspack.HashText(variantReal), RealEN: "Sell it."}},
	}
	idx, err := corpuspack.LoadIndex(corpus)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := variant.Validate(*vf, idx); err != nil {
		t.Fatalf("variant rejected: %v", err)
	}
	targets, err := RunAll(context.Background(), AllOptions{
		CorpusDir: corpus, LocresIn: resource, WorkDir: filepath.Join(dir, "work"), DistDir: filepath.Join(dir, "dist"), Version: "0.1.0",
		Filter: Filter{Platform: PlatformSwitch, Layout: LayoutEmulator},
	}, vf)
	if err != nil {
		t.Fatal(err)
	}
	byEdition := map[string]Target{}
	for _, tg := range targets {
		byEdition[tg.Edition] = tg
	}
	for _, ed := range []string{EditionCanonical, EditionNoHonorifics} {
		tg, ok := byEdition[ed]
		if !ok {
			t.Fatalf("%s target missing", ed)
		}
		if tg.State != StatePass {
			t.Fatalf("%s = %s (%s)", ed, tg.State, tg.Reason)
		}
	}
}

// TestCompositionStatusFallback pins the three composition rules: an unreviewed record without a real line keeps
// the official text, an unreviewed record with one takes it, and a KEEP without a real line keeps the official text.
func TestCompositionStatusFallback(t *testing.T) {
	dir := t.TempDir()
	corpus := filepath.Join(dir, "translations")
	jpA, offA := "買う", "Buy"
	jpB, offB, realB := "売る", "Sell", "Sell it"
	jpC, offC := "保つ", "Keep"
	src := corpuspack.File{
		SchemaVersion: corpuspack.SchemaVersion,
		Group:         corpuspack.Group{Type: "ui_label", Namespace: "ST_UI_Shop_Word"},
		Records: []corpuspack.Record{
			{RecordID: "u1", Source: corpuspack.Source{Set: "s", Namespace: "ST_UI_Shop_Word", Key: "A", JPSHA256: corpuspack.HashText(jpA), OfficialENSHA256: corpuspack.HashText(offA)},
				Text: corpuspack.Text{JP: &jpA, OfficialEN: &offA}, Review: corpuspack.Review{Status: corpuspack.StatusUnreviewed}, Function: "ui_label"},
			{RecordID: "u2", Source: corpuspack.Source{Set: "s", Namespace: "ST_UI_Shop_Word", Key: "B", JPSHA256: corpuspack.HashText(jpB), OfficialENSHA256: corpuspack.HashText(offB)},
				Text: corpuspack.Text{JP: &jpB, OfficialEN: &offB, RealEN: &realB}, Review: corpuspack.Review{Status: corpuspack.StatusUnreviewed}, Function: "ui_label"},
			{RecordID: "k1", Source: corpuspack.Source{Set: "s", Namespace: "ST_UI_Shop_Word", Key: "C", JPSHA256: corpuspack.HashText(jpC), OfficialENSHA256: corpuspack.HashText(offC)},
				Text: corpuspack.Text{JP: &jpC, OfficialEN: &offC}, Review: corpuspack.Review{Status: corpuspack.StatusKeep}, Function: "ui_label"},
		},
	}
	p := filepath.Join(corpus, "ui", "ST_UI_Shop_Word.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(src)
	if err := os.WriteFile(p, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := corpuspack.RefreshManifest(corpus); err != nil {
		t.Fatal(err)
	}
	resource := filepath.Join(dir, "Game.locres")
	in := locres.File{Version: 3, Entries: []locres.Entry{
		{Namespace: "ST_UI_Shop_Word", Key: "A", Text: "Buy", SourceHash: 1, NamespaceHash: 1, KeyHash: 11},
		{Namespace: "ST_UI_Shop_Word", Key: "B", Text: "Sell", SourceHash: 2, NamespaceHash: 1, KeyHash: 12},
		{Namespace: "ST_UI_Shop_Word", Key: "C", Text: "Keep", SourceHash: 3, NamespaceHash: 1, KeyHash: 13},
	}}
	lb, _ := locres.Write(in)
	if err := os.WriteFile(resource, lb, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), Options{CorpusDir: corpus, LocresIn: resource, OutDir: filepath.Join(dir, "out"), Version: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StatePass {
		t.Fatalf("composition = %s (%s)", rep.State, rep.Reason)
	}
	out, err := locres.Read(rep.Output)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range out.Entries {
		got[e.Key] = e.Text
	}
	if got["A"] != "Buy" {
		t.Fatalf("unreviewed without real_en = %q, want official Buy", got["A"])
	}
	if got["B"] != "Sell it" {
		t.Fatalf("unreviewed with real_en = %q, want the stored real line", got["B"])
	}
	if got["C"] != "Keep" {
		t.Fatalf("KEEP without real_en = %q, want official Keep", got["C"])
	}
}
