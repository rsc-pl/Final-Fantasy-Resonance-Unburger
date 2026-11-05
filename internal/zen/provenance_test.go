package zen_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unburger/internal/zen"
)

// TestCanonicalProvenanceHasNoApprovedClaims pins the honest provenance boundary: the canonical corpus may carry
// real translations and reviewed statuses, but no approval batch is supplied here, so none of those claims may be
// reported as approved.
func TestCanonicalProvenanceHasNoApprovedClaims(t *testing.T) {
	idx := requireCanonicalCorpus(t)
	en := corpusMonolith(t, idx, "real")
	rep, assessments, err := zen.BuildProvenance(en, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Records != idx.Stats.RealEN || rep.Records == 0 {
		t.Fatalf("provenance records = %d, canonical real_en = %d", rep.Records, idx.Stats.RealEN)
	}
	if rep.Approved != 0 {
		t.Fatalf("approved = %d, want 0 without an approval batch", rep.Approved)
	}
	if rep.Unapproved != rep.ReviewedStatuses {
		t.Fatalf("unapproved = %d, reviewed statuses = %d", rep.Unapproved, rep.ReviewedStatuses)
	}
	if rep.ReviewedStatuses+rep.UnreviewedRecords != rep.Records {
		t.Fatalf("status buckets do not add up: %+v", rep)
	}
	for _, a := range assessments {
		if a.Approved {
			t.Fatalf("%s claims approval without an approved batch", a.RecordID)
		}
	}
}

// TestProvenanceCountsUnapprovedClaims exercises the provenance report on a small record list: a status with no
// approval is counted as unapproved, and the note states the actual approval count instead of a hard-coded claim.
func TestProvenanceCountsUnapprovedClaims(t *testing.T) {
	dir := t.TempDir()
	en := []zen.AuditedRecord{
		{RecordID: "rec-1", Text: "Buy", ContextRef: "namespace=ST_Shop key=Buy", Status: zen.StatusUnreviewed, SpeakerConfidence: "none"},
		{RecordID: "rec-2", Text: "Sell", ContextRef: "namespace=ST_Shop key=Sell", Status: "KEEP", Reason: "reviewed", SpeakerConfidence: "none"},
	}
	b, err := json.Marshal(en)
	if err != nil {
		t.Fatal(err)
	}
	enPath := filepath.Join(dir, "en.json")
	if err := os.WriteFile(enPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	batches := filepath.Join(dir, "batches")
	if err := os.MkdirAll(batches, 0o755); err != nil {
		t.Fatal(err)
	}
	rep, assessments, err := zen.BuildProvenance(enPath, batches)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Records != 2 || rep.Approved != 0 || rep.ReviewedStatuses != 1 || rep.UnreviewedRecords != 1 {
		t.Fatalf("provenance counts = %+v", rep)
	}
	if rep.ByStatus[zen.StatusUnreviewed] != 1 || rep.ByStatus["KEEP"] != 1 {
		t.Fatalf("status census = %v", rep.ByStatus)
	}
	if rep.WithReason != 1 || rep.WithoutReason != 1 {
		t.Fatalf("reason counts = %d/%d", rep.WithReason, rep.WithoutReason)
	}
	for _, a := range assessments {
		if a.Approved {
			t.Fatalf("%s claims approval without an approved batch", a.RecordID)
		}
	}
	found := false
	for _, note := range rep.Notes {
		if strings.Contains(note, "0 batch file(s)") {
			found = true
		}
	}
	if !found {
		t.Fatalf("notes do not state the measured approval count: %v", rep.Notes)
	}
}

// TestCorpusTextsAreNeverEmpty protects the English fallback at the data layer: a present text is never an empty
// string, so a build cannot substitute an empty value for an entry that has a real or official counterpart.
func TestCorpusTextsAreNeverEmpty(t *testing.T) {
	idx := requireCanonicalCorpus(t)
	for _, r := range idx.ByID {
		for name, text := range map[string]*string{"jp": r.Text.JP, "official_en": r.Text.OfficialEN, "real_en": r.Text.RealEN} {
			if text != nil && *text == "" {
				t.Fatalf("%s: %s is present but empty", r.RecordID, name)
			}
		}
	}
}
