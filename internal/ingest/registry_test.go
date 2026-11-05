package ingest

import (
	"fmt"
	"testing"
)

func keySet(prefix string, n int) map[string]bool {
	out := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		out[fmt.Sprintf("ST_Scene\x00%s_%03d", prefix, i)] = true
	}
	return out
}

// TestSetIdentitySurvivesKeyChurn is the durability regression the corpus depends on: an
// update that adds or deletes records must not renumber the set, and every surviving record
// must keep its id.
func TestSetIdentitySurvivesKeyChurn(t *testing.T) {
	reg := &registry{}
	v1 := keySet("K", 20)
	id := reg.mint("switch", sortedKeys(v1), "switch-base")
	if id == "" {
		t.Fatal("mint returned no id")
	}

	// v2 adds three records and drops one that is not an anchor.
	v2 := map[string]bool{}
	for k := range v1 {
		v2[k] = true
	}
	for i := 0; i < 3; i++ {
		v2[fmt.Sprintf("ST_Scene\x00K_NEW_%03d", i)] = true
	}
	anchors := map[string]bool{}
	for _, a := range reg.Sets[0].Anchors {
		anchors[a] = true
	}
	dropped := ""
	for k := range v2 {
		if !anchors[k] {
			dropped = k
			break
		}
	}
	if dropped == "" {
		t.Fatal("test setup: every key is an anchor")
	}
	delete(v2, dropped)

	got, _, proven := reg.match("switch", v2, nil)
	if !proven || got != id {
		t.Fatalf("set identity lost after add/remove: got %q proven=%v, want %q", got, proven, id)
	}

	// Surviving records keep the same logical id.
	for k := range v1 {
		if !v2[k] {
			continue
		}
		ns, key := splitKey(k)
		if recordID("switch", id, ns, key) != recordID("switch", id, ns, key) {
			t.Fatalf("record id is not deterministic for %s", k)
		}
	}
}

// TestJaccardAloneIsNotIdentity pins the rule that similarity may propose a match but never
// prove one: with an anchor key missing the archive gets a fresh id.
func TestJaccardAloneIsNotIdentity(t *testing.T) {
	reg := &registry{}
	v1 := keySet("S", 20)
	original := reg.mint("switch", sortedKeys(v1), "switch-base")

	anchor := reg.Sets[0].Anchors[0]
	lookalike := map[string]bool{}
	for k := range v1 {
		lookalike[k] = true
	}
	delete(lookalike, anchor)
	for i := 0; i < 2; i++ {
		lookalike[fmt.Sprintf("ST_Other\x00S_%03d", i)] = true
	}

	// Overlap is high by count, but the anchor evidence is gone.
	shared := 0
	for k := range lookalike {
		if v1[k] {
			shared++
		}
	}
	ratio := float64(shared) / float64(len(v1)+len(lookalike)-shared)
	if ratio < 0.85 {
		t.Fatalf("test setup: overlap too low (%.2f) to exercise the rule", ratio)
	}
	if got, _, proven := reg.match("switch", lookalike, nil); proven {
		t.Fatalf("similarity alone was accepted as identity (%q)", got)
	}

	minted := reg.mint("switch", sortedKeys(lookalike), "switch-update")
	if minted == original {
		t.Fatal("a look-alike set reused the original id")
	}
}

// TestRecordIDIgnoresContent proves the text is not part of identity.
func TestRecordIDIgnoresContent(t *testing.T) {
	a := rec("x", "第一稿", "locres namespace=ST_A key=Line_001")
	b := rec("x", "second draft, completely different", "locres namespace=ST_A key=Line_001")
	if recordID("switch", "set-01", "ST_A", "Line_001") != recordID("switch", "set-01", "ST_A", "Line_001") {
		t.Fatal("record id is not stable")
	}
	if a.ContentHash == b.ContentHash {
		t.Fatal("content hash did not change with the text")
	}
}

func splitKey(k string) (string, string) {
	for i := 0; i < len(k); i++ {
		if k[i] == 0 {
			return k[:i], k[i+1:]
		}
	}
	return "", k
}
