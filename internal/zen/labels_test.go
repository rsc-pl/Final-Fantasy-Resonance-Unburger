package zen_test

import (
	"strings"
	"testing"

	"unburger/internal/zen"
)

// TestCollectTalkLabelsCorpus runs the whole-corpus walk and enforces the invariants that make the
// pairs trustworthy: a large share of tables parsed, every pair has a key and a plausible label, and
// the two confirmed scenes keep their exact speaker order.
func TestCollectTalkLabelsCorpus(t *testing.T) {
	paks, dec := fixture(t)
	res, err := zen.CollectTalkLabels(paks, dec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Tables < 800 {
		t.Fatalf("%d TalkEventRowData tables, expected >= 800", res.Tables)
	}
	if res.Parsed < 600 {
		t.Fatalf("only %d tables parsed of %d", res.Parsed, res.Tables)
	}
	if len(res.Items) < 8000 {
		t.Fatalf("%d pairs, expected >= 8000", len(res.Items))
	}
	if len(res.Failed) == 0 && res.Tables-res.Parsed > 0 {
		t.Fatalf("%d tables refused but no reason recorded", res.Tables-res.Parsed)
	}
	for _, it := range res.Items {
		if it.Key == "" || it.Label == "" {
			t.Fatalf("%+v: empty key or label", it)
		}
		if strings.ContainsAny(it.Label, "/._") {
			t.Fatalf("%s: label %q is an asset path or identifier, not a speaker label", it.Table, it.Label)
		}
		if it.Start < 0 || it.End <= it.Start {
			t.Fatalf("%s row %d: bad bounds %d..%d", it.Table, it.Row, it.Start, it.End)
		}
	}
	want := map[string]string{
		"SubEv_C09_Gilgamesh_01_001": "レイン",
		"SubEv_C09_Gilgamesh_01_004": "テロップ",
		"SubEv_C09_Gilgamesh_03_002": "ギルガメッシュ",
	}
	got := map[string]string{}
	for _, it := range res.Items {
		got[it.Key] = it.Label
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s -> %q, want %q", k, got[k], v)
		}
	}
	t.Logf("tables=%d parsed=%d rows=%d pairs=%d refused=%d", res.Tables, res.Parsed, res.Rows, len(res.Items), res.Tables-res.Parsed)
}

// TestLabelAgreementAcrossTables checks that a dialogue key never carries two different speaker
// labels in different tables. A single disagreement would mean the -8/-9 byte rule picked up two
// different fields, so this pins the rule rather than the numbers.
func TestLabelAgreementAcrossTables(t *testing.T) {
	paks, dec := fixture(t)
	labels, err := zen.CollectTalkLabels(paks, dec)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, it := range labels.Items {
		if prev, ok := seen[it.Key]; ok && prev != it.Label {
			t.Fatalf("key %s labelled %q in one table and %q in another", it.Key, prev, it.Label)
		}
		seen[it.Key] = it.Label
	}
	if len(seen) < 8000 {
		t.Fatalf("only %d distinct keys, expected the corpus-scale set", len(seen))
	}
	perTable := map[string]map[string]bool{}
	for _, it := range labels.Items {
		if perTable[it.Table] == nil {
			perTable[it.Table] = map[string]bool{}
		}
		perTable[it.Table][it.Label] = true
	}
	if len(perTable) < 600 {
		t.Fatalf("only %d tables carry labels, expected 600+", len(perTable))
	}
	t.Logf("keys=%d tables=%d labels=%d", len(seen), len(perTable), len(seen))
}
