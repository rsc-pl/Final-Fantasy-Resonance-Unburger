package corpus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Record struct {
	RecordID   string `json:"record_id"`
	Resource   string `json:"resource,omitempty"`
	Text       string `json:"text"`
	SourceHash string `json:"source_hash,omitempty"`
	ContextRef string `json:"context_ref,omitempty"`
	Status     string `json:"status,omitempty"`
	Reason     string `json:"reason,omitempty"`

	ContentHash   string `json:"content_hash,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`

	Scene             string `json:"scene,omitempty"`
	Seq               int    `json:"seq,omitempty"`
	PrevRecordID      string `json:"prev_record_id,omitempty"`
	NextRecordID      string `json:"next_record_id,omitempty"`
	Speaker           string `json:"speaker,omitempty"`
	Addressee         string `json:"addressee,omitempty"`
	SpeakerConfidence string `json:"speaker_confidence,omitempty"`
	SpeakerEvidence   string `json:"speaker_evidence,omitempty"`
}

var hex64 = regexp.MustCompile(`^[a-f0-9]{64}$`)

// decodeStrict unmarshals one record file and refuses unknown fields, so a record that drifts outside the
// schema fails here instead of silently carrying data nothing validates.
func decodeStrict(b []byte, into interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	return dec.Decode(&struct{}{}) // trailing content is an error
}

func Validate(root string) (map[string]int, error) {
	counts := map[string]int{}
	for _, lang := range []string{"jp", "loc", "official-en", "en"} {
		entries, e := os.ReadDir(filepath.Join(root, "translations", lang))
		if e != nil {
			return nil, e
		}
		seen := map[string]bool{}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			p := filepath.Join(root, "translations", lang, entry.Name())
			b, e := os.ReadFile(p)
			if e != nil {
				return nil, e
			}
			var rs []Record
			if e = json.Unmarshal(b, &rs); e != nil {
				return nil, fmt.Errorf("%s: %w", p, e)
			}
			for _, r := range rs {
				if r.RecordID == "" || strings.TrimSpace(r.Text) == "" {
					return nil, fmt.Errorf("%s: missing record ID or text", p)
				}
				if seen[r.RecordID] {
					return nil, fmt.Errorf("duplicate %s record ID %s", lang, r.RecordID)
				}
				seen[r.RecordID] = true
				if r.SourceHash != "" && !hex64.MatchString(r.SourceHash) {
					return nil, fmt.Errorf("%s: invalid source hash", r.RecordID)
				}
				if r.ContentHash != "" && !hex64.MatchString(r.ContentHash) {
					return nil, fmt.Errorf("%s: invalid content hash", r.RecordID)
				}
				// A real-translation record may only carry an editorial status that names a reviewer, or
				// none at all. UNREVIEWED is the honest default: the record exists, nobody has decided it.
				if lang == "en" && r.Status != "" {
					switch r.Status {
					case "UNREVIEWED":
						if strings.TrimSpace(r.Reason) != "" {
							return nil, fmt.Errorf("%s: an unreviewed record must not carry a review reason", r.RecordID)
						}
					case "KEEP", "CHANGE", "HOLD":
						if strings.TrimSpace(r.Reason) == "" {
							return nil, fmt.Errorf("%s: review status %s without a reason", r.RecordID, r.Status)
						}
					default:
						return nil, fmt.Errorf("%s: invalid review status %q", r.RecordID, r.Status)
					}
				}
			}
			counts[lang] += len(rs)
		}
	}
	return counts, nil
}
