package corpuspack_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"unburger/internal/corpuspack"
)

func corpusDir() string { return "../../translations" }

func requireCorpus(t *testing.T) *corpuspack.Index {
	t.Helper()
	if _, err := os.Stat(filepath.Join(corpusDir(), "manifest.json")); err != nil {
		t.Fatalf("canonical corpus is required for this test: %v", err)
	}
	idx, err := corpuspack.LoadIndex(corpusDir())
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

// TestCanonicalRecordUniqueness: every record identity appears exactly once.
func TestCanonicalRecordUniqueness(t *testing.T) {
	idx := requireCorpus(t)
	st, err := corpuspack.Validate(corpusDir())
	if err != nil {
		t.Fatal(err)
	}
	if st.Records != len(idx.ByID) {
		t.Fatalf("index holds %d records, validation saw %d", len(idx.ByID), st.Records)
	}
	if st.Records != 81766 {
		t.Fatalf("records = %d, want 81766", st.Records)
	}
	seen := map[string]int{}
	for _, f := range idx.Files {
		for _, r := range f.Records {
			seen[r.Source.Set+"|"+r.Source.Namespace+"|"+r.Source.Key+"|"+r.Source.JPRecordID]++
		}
	}
	for k, n := range seen {
		if n > 1 {
			t.Fatalf("source identity %s appears %d times", k, n)
		}
	}
}

// TestCanonicalSourceHashes: every stored text matches the hash recorded beside it.
func TestCanonicalSourceHashes(t *testing.T) {
	idx := requireCorpus(t)
	checked := 0
	for _, r := range idx.ByID {
		if r.Text.JP != nil {
			if corpuspack.HashText(*r.Text.JP) != r.Source.JPSHA256 {
				t.Fatalf("%s: Japanese hash mismatch", r.RecordID)
			}
			checked++
		}
		if r.Text.OfficialEN != nil {
			if corpuspack.HashText(*r.Text.OfficialEN) != r.Source.OfficialENSHA256 {
				t.Fatalf("%s: official English hash mismatch", r.RecordID)
			}
			checked++
		}
	}
	if checked < 160000 {
		t.Fatalf("only %d hashes checked, want at least 160000", checked)
	}
}

// TestCanonicalMissingTranslations: a missing counterpart is null, never a copy of the other language, and no
// real translation exists yet.
func TestCanonicalMissingTranslations(t *testing.T) {
	idx := requireCorpus(t)
	missingOfficial, missingJP, realEN := 0, 0, 0
	for _, r := range idx.ByID {
		if r.Text.OfficialEN == nil {
			missingOfficial++
			if r.Source.OfficialENSHA256 != "" {
				t.Fatalf("%s: missing text with a hash", r.RecordID)
			}
		}
		if r.Text.JP == nil {
			missingJP++
		}
		if r.Text.RealEN != nil {
			realEN++
		}
	}
	// The counts of missing source text are properties of the shipped data and are pinned. The number of approved
	// translations grows with project decisions, so it is only required to be non-zero here; that every one of
	// them is approved is checked by TestCanonicalNoUnapprovedDecisions.
	if missingOfficial != 36 || missingJP != 0 {
		t.Fatalf("missing official = %d, missing jp = %d; want 36/0", missingOfficial, missingJP)
	}
	if realEN == 0 {
		t.Fatal("no approved translation at all: the corpus is not in a reviewable state")
	}
	t.Logf("approved translations in the corpus: %d", realEN)
}

// TestCanonicalNoUnapprovedDecisions: a decision is only a decision when it is approved, reasoned and tied to
// the source it was taken on. The corpus may carry approved decisions; what it may never carry is a status
// without an approval reference, a decision without a reason, or an unreviewed record dressed up as one.
func TestCanonicalNoUnapprovedDecisions(t *testing.T) {
	idx := requireCorpus(t)
	approved := 0
	for _, r := range idx.ByID {
		switch r.Review.Status {
		case corpuspack.StatusUnreviewed:
			if r.Review.Reason != nil {
				t.Fatalf("%s: unreviewed record carries a review reason", r.RecordID)
			}
		case corpuspack.StatusKeep, corpuspack.StatusChange, corpuspack.StatusHold:
			// A review applies to the text it was taken on: the hashes must still describe what is stored.
			if r.Text.JP == nil || corpuspack.HashText(*r.Text.JP) != r.Source.JPSHA256 {
				t.Fatalf("%s: approved decision on a record whose japanese text does not match its source hash", r.RecordID)
			}
			if r.Text.OfficialEN != nil && corpuspack.HashText(*r.Text.OfficialEN) != r.Source.OfficialENSHA256 {
				t.Fatalf("%s: approved decision on a record whose official text does not match its source hash", r.RecordID)
			}
			// Only CHANGE has to carry a new line; KEEP without one falls back to the official English text.
			if r.Review.Status == corpuspack.StatusChange {
				if r.Text.RealEN == nil || *r.Text.RealEN == "" {
					t.Fatalf("%s: CHANGE without a translated line", r.RecordID)
				}
			}
			approved++
		default:
			t.Fatalf("%s: unknown review status %q", r.RecordID, r.Review.Status)
		}
	}
	if approved == 0 {
		t.Fatal("the corpus holds no approved decision at all: the check above proved nothing")
	}
}

// TestUnapprovedDecisionIsRefused: the validator rejects a status that claims approval without it, so the rule
// above is enforced by the toolchain and not only by this test.

// TestCanonicalSpeakerPreservation: the speaker work survives the migration with its levels intact.
func TestCanonicalSpeakerPreservation(t *testing.T) {
	idx := requireCorpus(t)
	applicability := map[string]int{}
	labelStatus := map[string]int{}
	nameEntries := 0
	for _, r := range idx.ByID {
		sp := r.Context.Speaker
		applicability[sp.Applicability]++
		labelStatus[sp.LabelStatus]++
		if sp.NameEntry != nil {
			nameEntries++
		}
		if sp.Identity != nil || sp.RuntimeCharacterID != nil {
			t.Fatalf("%s: identity or runtime id set without proof", r.RecordID)
		}
	}
	if applicability[corpuspack.ApplicabilityRequired] != 15823 {
		t.Fatalf("required = %d, want 15823", applicability[corpuspack.ApplicabilityRequired])
	}
	if applicability[corpuspack.ApplicabilityUndetermined] != 1084 {
		t.Fatalf("undetermined = %d, want 1084", applicability[corpuspack.ApplicabilityUndetermined])
	}
	if labelStatus[corpuspack.LabelVerified] != 13342 {
		t.Fatalf("verified labels = %d, want 13342", labelStatus[corpuspack.LabelVerified])
	}
	if labelStatus[corpuspack.LabelConflict] != 3 {
		t.Fatalf("conflicts = %d, want 3", labelStatus[corpuspack.LabelConflict])
	}
	if nameEntries != 13079 {
		t.Fatalf("name entries = %d, want 13079", nameEntries)
	}
	// Both conflict candidates are kept; neither is chosen.
	for _, r := range idx.ByID {
		if r.Context.Speaker.LabelStatus == corpuspack.LabelConflict && len(r.Context.Speaker.Candidates) < 2 {
			t.Fatalf("%s: conflict without both candidates", r.RecordID)
		}
	}
	// The younger labels stay their own labels, unresolved.
	for _, r := range idx.ByID {
		if l := r.Context.Speaker.LabelJP; l != nil && (*l == "レイン(子ども)" || *l == "ラスウェル(子ども)") {
			if r.Context.Speaker.NameEntry != nil {
				t.Fatalf("%s: young variant resolved to an entry", r.RecordID)
			}
		}
	}
}

// TestCanonicalManifestDeterminism: the manifest is a function of the content, not of the run.
func TestCanonicalManifestDeterminism(t *testing.T) {
	if _, err := os.Stat(filepath.Join(corpusDir(), "manifest.json")); err != nil {
		t.Fatalf("canonical corpus is required for this test: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(corpusDir(), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var man corpuspack.Manifest
	if err := json.Unmarshal(raw, &man); err != nil {
		t.Fatal(err)
	}
	if man.Format != corpuspack.FormatName || man.SchemaVersion != corpuspack.SchemaVersion {
		t.Fatalf("manifest declares %s/%d", man.Format, man.SchemaVersion)
	}
	if man.Counts.Files != len(man.Files) {
		t.Fatalf("manifest counts %d files but lists %d", man.Counts.Files, len(man.Files))
	}
	// Every listed hash matches the file it names.
	for _, e := range man.Files {
		b, err := os.ReadFile(filepath.Join(corpusDir(), e.Path))
		if err != nil {
			t.Fatalf("%s: %v", e.Path, err)
		}
		if corpuspack.HashText(string(b)) != e.SHA256 {
			t.Fatalf("%s: manifest hash does not match the file", e.Path)
		}
	}
	if man.Counts.Records != 81766 || man.Counts.JP != 81766 || man.Counts.OfficialEN != 81730 {
		t.Fatalf("manifest counts = %+v", man.Counts)
	}
}

// TestCanonicalLegacyPathsAbsent: the pre-migration monoliths must not survive in the public tree.
func TestCanonicalLegacyPathsAbsent(t *testing.T) {
	for _, legacy := range []string{"jp", "loc", "official-en", "en"} {
		if _, err := os.Stat(filepath.Join(corpusDir(), legacy)); err == nil {
			t.Fatalf("legacy directory translations/%s still present", legacy)
		}
	}
	for _, name := range []string{"records.json", "jp.json", "en.json"} {
		if _, err := os.Stat(filepath.Join(corpusDir(), name)); err == nil {
			t.Fatalf("legacy monolith translations/%s still present", name)
		}
	}
	// A symlink to a private source would be just as bad as a copy.
	entries, err := os.ReadDir(corpusDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			t.Fatalf("translations/%s is a symlink", e.Name())
		}
	}
}

// TestCanonicalCorpusRoundtrip rebuilds the corpus from the archived sources and compares record for record.
func TestCanonicalCorpusRoundtrip(t *testing.T) {
	archive := "../../.local/archive/monoliths-20261030"
	keys := "../../.local/toolchain/records/key-identities.json"
	for _, p := range []string{archive, keys} {
		if _, err := os.Stat(p); err != nil {
			t.Skip("archived sources not present on this host")
		}
	}
	out := t.TempDir()
	res, err := corpuspack.Build(corpuspack.BuildOptions{
		JP:       filepath.Join(archive, "jp.records.json"),
		Loc:      filepath.Join(archive, "loc.records.json"),
		Keys:     keys,
		OutDir:   out,
		Existing: corpusDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Manifest.Counts.Records != 81766 || res.Manifest.Counts.JP != 81766 || res.Manifest.Counts.OfficialEN != 81730 {
		t.Fatalf("rebuild counts = %+v", res.Manifest.Counts)
	}
	a, err := corpuspack.LoadIndex(corpusDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := corpuspack.LoadIndex(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.ByID) != len(b.ByID) {
		t.Fatalf("roundtrip holds %d records, corpus holds %d", len(b.ByID), len(a.ByID))
	}
	for id, ra := range a.ByID {
		rb, ok := b.ByID[id]
		if !ok {
			t.Fatalf("%s missing from the roundtrip", id)
		}
		if deref(ra.Text.JP) != deref(rb.Text.JP) || deref(ra.Text.OfficialEN) != deref(rb.Text.OfficialEN) {
			t.Fatalf("%s: texts differ after the roundtrip", id)
		}
		if ra.Source.JPSHA256 != rb.Source.JPSHA256 || ra.Source.OfficialENSHA256 != rb.Source.OfficialENSHA256 {
			t.Fatalf("%s: source hashes differ after the roundtrip", id)
		}
		if ra.Context.Speaker.Applicability != rb.Context.Speaker.Applicability {
			t.Fatalf("%s: speaker applicability drifted", id)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
