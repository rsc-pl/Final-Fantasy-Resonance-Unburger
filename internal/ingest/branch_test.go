package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

func writeManifest(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "Manifest_UFSFiles_Switch.txt")
	body := ""
	for _, l := range lines {
		body += l + "\t2026-07-15T09:55:07.750Z\r\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSceneAssetsExactMatchWins(t *testing.T) {
	m := writeManifest(t,
		"FFRS/Content/Sequencer/Event/LegendEvent/Int_LegendEV_01/Choice1/SEQ_A.uasset",
		"FFRS/Content/Sequencer/Event/LegendEvent/LegendEV_01/SEQ_B.uasset",
		"FFRS/Content/Sequencer/Event/LegendEvent/LegendEV_01/SEQ_C.uasset",
	)
	idx := LoadAssetIndex(m)
	if len(idx) != 2 {
		t.Fatalf("event directories: %d, want 2", len(idx))
	}
	jp := []Record{
		{RecordID: "a", Scene: "ST_LegendEV_01", Text: "x"},
		{RecordID: "b", Scene: "ST_UnknownScene", Text: "y"},
	}
	annotated, resolved := AnnotateSceneAssets(jp, nil, idx)
	if resolved != 1 || annotated != 1 {
		t.Fatalf("resolved=%d annotated=%d, want 1/1", resolved, annotated)
	}
	if jp[0].SceneAssetDir != "FFRS/Content/Sequencer/Event/LegendEvent/LegendEV_01" {
		t.Fatalf("wrong directory: %s", jp[0].SceneAssetDir)
	}
	if jp[0].SceneConfidence != "asset-path" {
		t.Fatalf("confidence: %s", jp[0].SceneConfidence)
	}
	// A single Choice directory under the matched event is branch evidence.
	if jp[0].Branch != "" {
		t.Fatalf("branch assigned without a Choice directory in the matched event: %q", jp[0].Branch)
	}
	if jp[1].SceneAssetDir != "" {
		t.Fatal("unknown scene was given asset evidence")
	}
	if jp[0].Speaker != "" || jp[0].Addressee != "" {
		t.Fatal("speaker must not be derived from an asset path")
	}
}

func TestSceneAssetsAmbiguityIsRefused(t *testing.T) {
	// Two directories carry the scene token at the last path segment: nothing may choose
	// between them, even though both are plausible.
	m := writeManifest(t,
		"FFRS/Content/Sequencer/Event/A/LegendEV_02/SEQ_A.uasset",
		"FFRS/Content/Sequencer/Event/B/LegendEV_02/SEQ_B.uasset",
	)
	idx := LoadAssetIndex(m)
	jp := []Record{{RecordID: "a", Scene: "ST_LegendEV_02", Text: "x"}}
	if _, resolved := AnnotateSceneAssets(jp, nil, idx); resolved != 0 {
		t.Fatal("ambiguous scene token was resolved to one directory")
	}
	if jp[0].SceneAssetDir != "" || jp[0].SceneConfidence != "" {
		t.Fatal("ambiguous match produced evidence")
	}
}

func TestBranchOnlyFromSingleChoiceDir(t *testing.T) {
	m := writeManifest(t,
		"FFRS/Content/Sequencer/Event/Event/LegendEV_03/Choice2/SEQ_A.uasset",
		"FFRS/Content/Sequencer/Event/Event/LegendEV_03/Choice2/SEQ_B.uasset",
	)
	idx := LoadAssetIndex(m)
	jp := []Record{{RecordID: "a", Scene: "ST_LegendEV_03", Text: "x"}}
	AnnotateSceneAssets(jp, nil, idx)
	if jp[0].Branch == "" || jp[0].BranchConfidence != "asset-path" {
		t.Fatalf("single Choice directory not recorded: %+v", jp[0])
	}
}
