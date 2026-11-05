package zen_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"unburger/internal/zen"
)

// TestNameGrammaticalSlots records a structural fact that changes how a name audit must be read: the
// localisation stores every item and skill name in five slots — the bare name plus the _sg, _pl, _det_sg and
// _det_pl forms. Variants such as Arcturus/Arcturi and iron armor/iron armors are therefore grammar slots,
// not inconsistent translations, and comparing them as if they were alternatives invents errors.
func TestNameGrammaticalSlots(t *testing.T) {
	loc := corpusMonolith(t, requireCanonicalCorpus(t), "official")
	raw, err := os.ReadFile(loc)
	if err != nil {
		t.Fatal(err)
	}
	var recs []struct {
		ContextRef string `json:"context_ref"`
	}
	if err := json.Unmarshal(raw, &recs); err != nil {
		t.Fatal(err)
	}
	suffix := regexp.MustCompile(`_(det_pl|det_sg|pl|sg)$`)
	counts := map[string]int{}
	bases := map[string]map[string]bool{}
	for _, r := range recs {
		k := ""
		for _, f := range strings.Fields(r.ContextRef) {
			if strings.HasPrefix(f, "key=") {
				k = strings.TrimPrefix(f, "key=")
			}
		}
		m := suffix.FindStringSubmatch(k)
		if m == nil {
			continue
		}
		counts[m[1]]++
		base := strings.TrimSuffix(k, m[0])
		if bases[base] == nil {
			bases[base] = map[string]bool{}
		}
		bases[base][m[1]] = true
	}
	for _, form := range []string{"sg", "pl", "det_sg", "det_pl"} {
		if counts[form] != 510 {
			t.Fatalf("%s slots = %d, want 510", form, counts[form])
		}
	}
	complete := 0
	for _, forms := range bases {
		if len(forms) == 4 {
			complete++
		}
	}
	if complete != 510 {
		t.Fatalf("%d names carry all four grammatical slots, want 510", complete)
	}
}

// TestSkillNameTermFromCorpus pins the proposal, not a decision: the 52 Transfiguration rows and the 26 names
// with no rendering live in the batch file, and the active record keeps every one of them UNREVIEWED.
func TestSkillNameTermFromCorpus(t *testing.T) {
	batch := "../../.local/toolchain/tests/audit-skillname-pass6.json"
	if _, err := os.Stat(batch); err != nil {
		t.Skip("batch not present on this host")
	}
	raw, err := os.ReadFile(batch)
	if err != nil {
		t.Fatal(err)
	}
	var b zen.AuditBatch
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	changed, pending := 0, 0
	for _, d := range b.Decisions {
		if d.Status == "CHANGE" {
			if d.Text != "Transfiguration" {
				t.Fatalf("%s: proposal %q is not the established term", d.Key, d.Text)
			}
			changed++
			continue
		}
		pending++
	}
	if changed != 52 || pending != 26 {
		t.Fatalf("skill-name proposals = %d changed / %d pending, want 52/26", changed, pending)
	}
}
