package zen_test

import (
	"encoding/json"
	"os"
	"testing"

	"unburger/internal/zen"
)

// TestAuditBatch is the regression for the first audit pass: every decision must join both corpora, carry a
// legal status and a reason, and the written record must stay inside the schema's fields.
func TestAuditBatch(t *testing.T) {
	batch := "../../.local/toolchain/tests/audit-ui-pass1.json"
	if _, err := os.Stat(batch); err != nil {
		t.Skip("optional private audit batch not present on this host")
	}
	idx := requireCanonicalCorpus(t)
	jp := corpusMonolith(t, idx, "jp")
	loc := corpusMonolith(t, idx, "official")
	b, err := zen.LoadAuditBatch(batch)
	if err != nil {
		t.Fatal(err)
	}
	// The private batch also carries a pending editorial decision whose status is outside the public record
	// schema. ApplyAudit must refuse it; the supported decisions must still join and produce schema-valid records.
	applicable := make([]zen.AuditDecision, 0, len(b.Decisions))
	var unsupported []string
	for _, d := range b.Decisions {
		switch d.Status {
		case "KEEP", "CHANGE", "HOLD":
			applicable = append(applicable, d)
		default:
			unsupported = append(unsupported, d.Status)
		}
	}
	if len(unsupported) != 1 {
		t.Fatalf("unsupported statuses = %v, want exactly one", unsupported)
	}
	if _, err := zen.ApplyAudit(zen.AuditBatch{Pass: b.Pass, Family: b.Family, Decisions: []zen.AuditDecision{{
		Namespace: "ST_UI_Guide", Key: "KeyGuideBattleLimit", Status: unsupported[0], Reason: "test",
	}}}, jp, loc); err == nil {
		t.Fatalf("status %q outside the public schema was accepted", unsupported[0])
	}
	b.Decisions = applicable
	recs, err := zen.ApplyAudit(b, jp, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != len(applicable) {
		t.Fatalf("records = %d, want %d", len(recs), len(applicable))
	}
	allowed := map[string]bool{
		"record_id": true, "resource": true, "text": true, "source_hash": true, "content_hash": true,
		"source_version": true, "context_ref": true, "status": true, "reason": true, "scene": true,
		"seq": true, "prev_record_id": true, "next_record_id": true, "speaker": true,
		"addressee": true, "speaker_confidence": true, "speaker_evidence": true, "branch": true,
		"branch_evidence": true, "branch_confidence": true, "scene_asset_dir": true,
		"scene_evidence": true, "scene_confidence": true,
	}
	counts := map[string]int{}
	for _, r := range recs {
		counts[r.Status]++
		if r.Reason == "" {
			t.Fatalf("%s: status without a reason", r.RecordID)
		}
		if r.Text == "" {
			t.Fatalf("%s: empty text", r.RecordID)
		}
		if r.Speaker != "" {
			t.Fatalf("%s: an interface label must not carry a speaker", r.RecordID)
		}
		// Marshal and check the field names against the schema's whitelist.
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for k := range m {
			if !allowed[k] {
				t.Fatalf("%s: field %q is outside the record schema", r.RecordID, k)
			}
		}
	}
	if counts["KEEP"] != 13 || counts["HOLD"] != 1 {
		t.Fatalf("status counts = %v, want 13 KEEP and 1 HOLD", counts)
	}
}

// TestAuditRejectsBadDecision makes sure a pass cannot silently accept an impossible decision.
func TestAuditRejectsBadDecision(t *testing.T) {
	idx := requireCanonicalCorpus(t)
	jp := corpusMonolith(t, idx, "jp")
	loc := corpusMonolith(t, idx, "official")
	bad := zen.AuditBatch{Pass: "x", Family: "x", Decisions: []zen.AuditDecision{{
		Namespace: "ST_UI_Word", Key: "NoSuchKey", Status: "KEEP", Reason: "test",
	}}}
	if _, err := zen.ApplyAudit(bad, jp, loc); err == nil {
		t.Fatal("a decision naming a key that does not exist was accepted")
	}
	bad.Decisions[0].Key = "Ability_en"
	bad.Decisions[0].Status = "MAYBE"
	if _, err := zen.ApplyAudit(bad, jp, loc); err == nil {
		t.Fatal("an unknown status was accepted")
	}
}
