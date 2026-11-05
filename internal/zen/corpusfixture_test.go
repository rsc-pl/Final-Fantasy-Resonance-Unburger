package zen_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"unburger/internal/corpuspack"
)

// canonicalDir is the sharded corpus. It is part of the public tree, so a missing corpus is a failed mandatory
// fixture, never a successful skip.
const canonicalDir = "../../translations"

type corpusMonolithRecord struct {
	RecordID          string `json:"record_id,omitempty"`
	Resource          string `json:"resource,omitempty"`
	Text              string `json:"text"`
	ContextRef        string `json:"context_ref"`
	Status            string `json:"status,omitempty"`
	Reason            string `json:"reason,omitempty"`
	SpeakerConfidence string `json:"speaker_confidence,omitempty"`
}

func requireCanonicalCorpus(t *testing.T) *corpuspack.Index {
	t.Helper()
	if _, err := os.Stat(filepath.Join(canonicalDir, "manifest.json")); err != nil {
		t.Fatalf("canonical corpus is required for this test: %v", err)
	}
	idx, err := corpuspack.LoadIndex(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func corpusContextRef(namespace, key string) string {
	return "locres namespace=" + namespace + " key=" + key
}

// corpusMonolith writes the requested text column of the canonical corpus as the flat record list the legacy
// analysis helpers accept. It keeps the test coverage on the real corpus without restoring a removed layout.
func corpusMonolith(t *testing.T, idx *corpuspack.Index, column string) string {
	t.Helper()
	var recs []corpusMonolithRecord
	for _, id := range idx.Order {
		r := idx.ByID[id]
		var text string
		switch column {
		case "jp":
			if r.Text.JP == nil {
				continue
			}
			text = *r.Text.JP
		case "official":
			if r.Text.OfficialEN == nil {
				continue
			}
			text = *r.Text.OfficialEN
		case "real":
			if r.Text.RealEN == nil {
				continue
			}
			text = *r.Text.RealEN
		default:
			t.Fatalf("unknown corpus column %q", column)
		}
		recs = append(recs, corpusMonolithRecord{
			RecordID:          r.RecordID,
			Resource:          r.Source.Set,
			Text:              text,
			ContextRef:        corpusContextRef(r.Source.Namespace, r.Source.Key),
			Status:            r.Review.Status,
			Reason:            derefString(r.Review.Reason),
			SpeakerConfidence: "none",
		})
	}
	b, err := json.Marshal(recs)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), column+".json")
	if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
