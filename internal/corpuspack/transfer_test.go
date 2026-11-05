package corpuspack_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"unburger/internal/corpuspack"
)

// fixture writes a two-record corpus so export and import can be exercised without touching the real tree.
func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	jp := "こんにちは。"
	en := "Hello."
	real := "Hi."
	_ = real
	f := corpuspack.File{
		SchemaVersion: corpuspack.SchemaVersion,
		Group:         corpuspack.Group{Type: "dialogue_line", Namespace: "ST_Test_01"},
		Records: []corpuspack.Record{
			{
				RecordID: "jp-1",
				Source: corpuspack.Source{
					Set: "set-02", Namespace: "ST_Test_01", Key: "Test_001",
					JPRecordID: "jp-1", OfficialENRecordID: "en-1",
					JPSHA256: corpuspack.HashText(jp), OfficialENSHA256: corpuspack.HashText(en),
				},
				Text:     corpuspack.Text{JP: &jp, OfficialEN: &en},
				Review:   corpuspack.Review{Status: corpuspack.StatusUnreviewed},
				Function: "dialogue_line",
				Context:  corpuspack.Context{Speaker: corpuspack.Speaker{Applicability: corpuspack.ApplicabilityRequired, LabelStatus: corpuspack.LabelVerified}},
			},
			{
				RecordID: "jp-2",
				Source: corpuspack.Source{
					Set: "set-02", Namespace: "ST_Test_01", Key: "Test_002",
					JPRecordID: "jp-2",
					JPSHA256:   corpuspack.HashText("さようなら。"),
				},
				Text:     corpuspack.Text{JP: strptr("さようなら。")},
				Review:   corpuspack.Review{Status: corpuspack.StatusUnreviewed},
				Function: "dialogue_line",
				Context:  corpuspack.Context{Speaker: corpuspack.Speaker{Applicability: corpuspack.ApplicabilityRequired, LabelStatus: corpuspack.LabelUnknown}},
			},
		},
	}
	path := filepath.Join(dir, "dialogue", "unassigned", "ST_Test_01.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, err := jsonMarshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := corpuspack.RefreshManifest(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func strptr(s string) *string { return &s }

func jsonMarshal(v interface{}) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func writeFileJSON(path string, v interface{}) error {
	b, err := jsonMarshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func writeAnyJSON(path string, v interface{}) error { return writeFileJSON(path, v) }

// TestCanonicalExportImport exercises the whole transfer path on a real file: export a scene, add an approved
// translation, refuse it while the hashes disagree, then apply it and check that only the real translation moved.
func TestCanonicalExportImport(t *testing.T) {
	dir := fixture(t)
	if _, err := corpuspack.Validate(dir); err != nil {
		t.Fatalf("fixture does not validate: %v", err)
	}
	out := filepath.Join(t.TempDir(), "export.json")
	exp, err := corpuspack.Export(corpuspack.ExportOptions{Dir: dir, Namespace: []string{"ST_Test_01"}, Out: out})
	if err != nil {
		t.Fatal(err)
	}
	if len(exp.Records) != 2 {
		t.Fatalf("exported %d records, want 2", len(exp.Records))
	}

	// An unapproved patch is refused outright.
	patch := corpuspack.File{SchemaVersion: corpuspack.SchemaVersion, Group: exp.Group, Records: []corpuspack.Record{
		{RecordID: "jp-1", Source: exp.Records[0].Source, Text: corpuspack.Text{RealEN: strptr("Hi.")},
			Review: corpuspack.Review{Status: corpuspack.StatusChange}},
	}}
	patchPath := filepath.Join(t.TempDir(), "patch.json")
	if err := writeFileJSON(patchPath, patch); err != nil {
		t.Fatal(err)
	}
	if _, err := corpuspack.Import(corpuspack.ImportOptions{Dir: dir, Patch: patchPath}); err == nil {
		t.Fatal("an import without an approval reference was accepted")
	}

	// A patch whose source hash disagrees is refused.
	bad := corpuspack.Record{RecordID: "jp-1", Source: exp.Records[0].Source, Text: corpuspack.Text{RealEN: strptr("Hi.")},
		Review: corpuspack.Review{Status: corpuspack.StatusChange, Reason: strptr("checked")}}
	bad.Source.OfficialENSHA256 = corpuspack.HashText("different text")
	badPatch := corpuspack.File{SchemaVersion: corpuspack.SchemaVersion, Group: exp.Group, Records: []corpuspack.Record{bad}}
	if err := writeFileJSON(patchPath, badPatch); err != nil {
		t.Fatal(err)
	}
	if _, err := corpuspack.Import(corpuspack.ImportOptions{Dir: dir, Patch: patchPath}); err == nil {
		t.Fatal("an import with a stale source hash was accepted")
	}

	// A dry run changes nothing.
	good := corpuspack.Record{RecordID: "jp-1", Source: exp.Records[0].Source, Text: corpuspack.Text{RealEN: strptr("Hi.")},
		Review: corpuspack.Review{Status: corpuspack.StatusChange, Reason: strptr("checked")}}
	goodPatch := corpuspack.File{SchemaVersion: corpuspack.SchemaVersion, Group: exp.Group, Records: []corpuspack.Record{good}}
	if err := writeFileJSON(patchPath, goodPatch); err != nil {
		t.Fatal(err)
	}
	res, err := corpuspack.Import(corpuspack.ImportOptions{Dir: dir, Patch: patchPath, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 {
		t.Fatalf("dry run applied %d", res.Applied)
	}
	idx, _ := corpuspack.LoadIndex(dir)
	if idx.ByID["jp-1"].Text.RealEN != nil {
		t.Fatal("a dry run wrote to the corpus")
	}

	// The real import applies, and leaves the source texts alone.
	if _, err := corpuspack.Import(corpuspack.ImportOptions{Dir: dir, Patch: patchPath}); err != nil {
		t.Fatal(err)
	}
	idx, _ = corpuspack.LoadIndex(dir)
	rec := idx.ByID["jp-1"]
	if rec.Text.RealEN == nil || *rec.Text.RealEN != "Hi." {
		t.Fatal("the real translation was not applied")
	}
	if deref(rec.Text.JP) != "こんにちは。" || deref(rec.Text.OfficialEN) != "Hello." {
		t.Fatal("import changed a source text")
	}
	if rec.Review.Status != corpuspack.StatusChange || rec.Review.Reason == nil {
		t.Fatal("import did not record the approval")
	}
	if _, err := corpuspack.Validate(dir); err != nil {
		t.Fatalf("corpus does not validate after import: %v", err)
	}
}

// TestCanonicalUpdatePreservesRealTL: rebuilding the corpus from sources must not drop an imported translation.
func TestCanonicalUpdatePreservesRealTL(t *testing.T) {
	dir := fixture(t)
	out := filepath.Join(t.TempDir(), "export.json")
	exp, err := corpuspack.Export(corpuspack.ExportOptions{Dir: dir, Namespace: []string{"ST_Test_01"}, Out: out})
	if err != nil {
		t.Fatal(err)
	}
	patch := corpuspack.File{SchemaVersion: corpuspack.SchemaVersion, Group: exp.Group, Records: []corpuspack.Record{
		{RecordID: "jp-1", Source: exp.Records[0].Source, Text: corpuspack.Text{RealEN: strptr("Hi.")},
			Review: corpuspack.Review{Status: corpuspack.StatusChange, Reason: strptr("checked")}},
	}}
	patchPath := filepath.Join(t.TempDir(), "patch.json")
	if err := writeFileJSON(patchPath, patch); err != nil {
		t.Fatal(err)
	}
	if _, err := corpuspack.Import(corpuspack.ImportOptions{Dir: dir, Patch: patchPath}); err != nil {
		t.Fatal(err)
	}
	// Rebuild from the same sources into a new tree, carrying the existing corpus's translations.
	src := filepath.Join(t.TempDir(), "sources")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	jpRecs := []map[string]string{
		{"record_id": "jp-1", "text": "こんにちは。", "context_ref": "namespace=ST_Test_01 key=Test_001"},
		{"record_id": "jp-2", "text": "さようなら。", "context_ref": "namespace=ST_Test_01 key=Test_002"},
	}
	if err := writeAnyJSON(filepath.Join(src, "jp.json"), jpRecs); err != nil {
		t.Fatal(err)
	}
	locRecs := []map[string]string{
		{"record_id": "en-1", "text": "Hello.", "context_ref": "namespace=ST_Test_01 key=Test_001"},
	}
	if err := writeAnyJSON(filepath.Join(src, "loc.json"), locRecs); err != nil {
		t.Fatal(err)
	}
	keys := map[string]interface{}{"bindings": []interface{}{}}
	if err := writeAnyJSON(filepath.Join(src, "keys.json"), keys); err != nil {
		t.Fatal(err)
	}
	newDir := filepath.Join(t.TempDir(), "rebuilt")
	if _, err := corpuspack.Build(corpuspack.BuildOptions{
		JP: filepath.Join(src, "jp.json"), Loc: filepath.Join(src, "loc.json"),
		Keys: filepath.Join(src, "keys.json"), OutDir: newDir, Existing: dir,
	}); err != nil {
		t.Fatal(err)
	}
	idx, err := corpuspack.LoadIndex(newDir)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := idx.ByID["jp-1"]
	if !ok {
		t.Fatal("the rebuilt corpus lost the record")
	}
	if rec.Text.RealEN == nil || *rec.Text.RealEN != "Hi." {
		t.Fatal("the rebuild dropped the real translation")
	}
	if rec.Review.Status != corpuspack.StatusChange || rec.Review.Reason == nil {
		t.Fatal("the rebuild dropped the review")
	}
}
