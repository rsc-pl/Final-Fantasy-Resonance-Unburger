package zen_test

import (
	"testing"

	"unburger/internal/zen"
)

// TestGlossaryNeedsEvidence pins the rule that a term pair enters the register only when the corpus itself
// supports it, and that a pair the corpus renders differently is refused rather than assumed.
func TestGlossaryNeedsEvidence(t *testing.T) {
	idx := requireCanonicalCorpus(t)
	jp := corpusMonolith(t, idx, "jp")
	loc := corpusMonolith(t, idx, "official")
	g, err := zen.BuildGlossary(jp, loc, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Entries) != 21 || len(g.Unresolved) != 0 {
		t.Fatalf("entries/unresolved = %d/%d, want 21/0", len(g.Entries), len(g.Unresolved))
	}
	byJP := map[string]zen.GlossaryEntry{}
	for _, e := range g.Entries {
		if e.Evidence < 3 {
			t.Fatalf("%s: evidence %d below the minimum", e.JP, e.Evidence)
		}
		if len(e.Examples) == 0 {
			t.Fatalf("%s: no supporting record", e.JP)
		}
		byJP[e.JP] = e
	}
	// The corpus renders 敵単体 as "an enemy"; a register entry claiming "one enemy" would be unsupported.
	if e, ok := byJP["敵単体"]; !ok || e.EN != "an enemy" || e.Evidence != 177 {
		t.Fatalf("敵単体 = %+v, want an enemy with 177 records", e)
	}
	// The agility stat keeps a project decision instead of being declared settled.
	if e := byJP["素早さ"]; e.Status != "review_pending" {
		t.Fatalf("素早さ status = %q, want review_pending", e.Status)
	}
}
