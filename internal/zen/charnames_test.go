package zen_test

import (
	"os"
	"testing"

	"unburger/internal/zen"
)

// TestCharNameIdentity pins the character-name resolution against the real corpus. The identifiers come
// from ST_CharaName by exact string equality, so this test also fixes the three outcomes that must not
// drift: a unique match, an ambiguous name, and a label with no character behind it.
func TestCharNameIdentity(t *testing.T) {
	speakers := "../../.local/toolchain/records/speakers.json"
	if _, err := os.Stat(speakers); err != nil {
		t.Skip("optional private speaker record not present on this host")
	}
	corpus := corpusMonolith(t, requireCanonicalCorpus(t), "jp")
	idx, err := zen.LoadCharNames(corpus)
	if err != nil {
		t.Fatal(err)
	}
	if idx.EntryCount() != 198 {
		t.Fatalf("character-name entries = %d, want 198", idx.EntryCount())
	}
	labels, err := zen.SpeakerLabels(speakers)
	if err != nil {
		t.Fatal(err)
	}
	rep := zen.BuildIdentityReport(idx, labels)
	if rep.Unique != 180 || rep.Ambiguous != 4 || rep.Missing != 47 {
		t.Fatalf("unique/ambiguous/missing = %d/%d/%d, want 180/4/47", rep.Unique, rep.Ambiguous, rep.Missing)
	}
	if rep.KeysWithIdentity != 13378 || rep.KeysTotal != 14831 {
		t.Fatalf("keys with identity = %d of %d, want 13378 of 14831", rep.KeysWithIdentity, rep.KeysTotal)
	}
	byLabel := map[string]zen.Identity{}
	for _, id := range rep.Identities {
		byLabel[id.Label] = id
	}
	if got := byLabel["レイン"]; got.State != zen.IdentityUnique || got.ID != "CharaName0002" {
		t.Fatalf("レイン resolved to %+v, want unique CharaName0002", got)
	}
	if got := byLabel["空白"]; got.State != zen.IdentityMissing {
		t.Fatalf("空白 resolved to %+v, want missing: it is the blank-label marker", got)
	}
	// A name carried by two entries must stay unresolved rather than pick one.
	ambiguous := 0
	for _, id := range rep.Identities {
		if id.State == zen.IdentityAmbiguous {
			if len(id.Candidates) < 2 || id.ID != "" {
				t.Fatalf("ambiguous identity %+v carries an id or too few candidates", id)
			}
			ambiguous++
		}
	}
	if ambiguous != 4 {
		t.Fatalf("ambiguous identities = %d, want 4", ambiguous)
	}
}
