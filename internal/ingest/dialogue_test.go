package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func rec(id, text, ctx string) Record {
	sum := sha256.Sum256([]byte(text))
	return Record{RecordID: id, Text: text, ContextRef: ctx, ContentHash: hex.EncodeToString(sum[:])}
}

func TestAnnotateDialogueOrdersAndLinks(t *testing.T) {
	jp := []Record{
		rec("id-c", "third", "locres namespace=ST_A key=Scene_0003"),
		rec("id-a", "first", "locres namespace=ST_A key=Scene_0001"),
		rec("id-b", "second", "locres namespace=ST_A key=Scene_0002"),
		rec("id-x", "not dialogue", "locres namespace=DT_Table key=Row_0001"),
	}
	loc := append([]Record(nil), jp...)
	AnnotateDialogue(jp, loc)

	byID := map[string]Record{}
	for _, r := range jp {
		byID[r.RecordID] = r
	}
	if byID["id-a"].Seq != 1 || byID["id-a"].NextRecordID != "id-b" || byID["id-a"].PrevRecordID != "" {
		t.Fatalf("first line wrong: %+v", byID["id-a"])
	}
	if byID["id-b"].PrevRecordID != "id-a" || byID["id-b"].NextRecordID != "id-c" {
		t.Fatalf("middle line wrong: %+v", byID["id-b"])
	}
	if byID["id-c"].NextRecordID != "" {
		t.Fatalf("last line has a successor: %+v", byID["id-c"])
	}
	if byID["id-x"].Scene != "" {
		t.Fatal("non-dialogue namespace treated as a dialogue scene")
	}
	if byID["id-a"].Speaker != "UNKNOWN" || byID["id-a"].SpeakerConfidence != "none" {
		t.Fatal("speaker must stay UNKNOWN without asset evidence")
	}

	matched := 0
	for _, r := range loc {
		if r.Scene == "ST_A" && r.Speaker == "UNKNOWN" && r.Seq > 0 {
			matched++
		}
	}
	if matched != 3 {
		t.Fatalf("official translation rows not annotated: %d", matched)
	}
}

func TestAnnotateIsStableAcrossReruns(t *testing.T) {
	build := func() []Record {
		return []Record{
			rec("id-2", "b", "locres namespace=ST_B key=K_10"),
			rec("id-1", "a", "locres namespace=ST_B key=K_2"),
		}
	}
	first := build()
	AnnotateDialogue(first, nil)
	second := build()
	AnnotateDialogue(second, nil)
	for i := range first {
		if first[i].Seq != second[i].Seq || first[i].PrevRecordID != second[i].PrevRecordID ||
			first[i].NextRecordID != second[i].NextRecordID {
			t.Fatalf("annotation is not deterministic at %d", i)
		}
	}
	// K_2 must sort before K_10 by numeric value, not lexicographically by string.
	byID := map[string]int{}
	for _, r := range first {
		byID[r.RecordID] = r.Seq
	}
	if byID["id-1"] != 2 || byID["id-2"] != 10 {
		t.Fatalf("sequence is not numeric: %v", byID)
	}
}
