package zen

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// AuditedRecord is one entry of the canonical RealTL record: the English text with the status this pass
// assigned, plus the reference back to the source record it was read from.
type AuditedRecord struct {
	RecordID string `json:"record_id"`
	Resource string `json:"resource,omitempty"`
	Text     string `json:"text"`
	// ContextRef points at the source record the English was audited against (namespace and key); the
	// canonical record carries no copy of the Japanese, which lives in its own corpus file.
	ContextRef string `json:"context_ref,omitempty"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
	Scene      string `json:"scene,omitempty"`
	// Speaker is left empty unless an asset-level source proves it; a label is not an identity.
	Speaker string `json:"speaker,omitempty"`
	// SpeakerConfidence follows the schema's enum: none, namespace, asset, runtime.
	SpeakerConfidence string `json:"speaker_confidence"`
}

// AuditDecision is one judgement made by the reviewer for a specific (namespace, key).
type AuditDecision struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	// Text, when set, replaces the English text; when empty the official text is kept verbatim.
	Text string `json:"text,omitempty"`
}

// AuditBatch is the input file for one limited audit pass.
type AuditBatch struct {
	Pass      string          `json:"pass"`
	Family    string          `json:"family"`
	Decisions []AuditDecision `json:"decisions"`
}

// LoadAuditBatch reads a batch of decisions.
func LoadAuditBatch(path string) (AuditBatch, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return AuditBatch{}, err
	}
	var b AuditBatch
	if err := json.Unmarshal(raw, &b); err != nil {
		return AuditBatch{}, fmt.Errorf("audit batch %s: %w", path, err)
	}
	if len(b.Decisions) == 0 {
		return AuditBatch{}, fmt.Errorf("audit batch %s: no decisions", path)
	}
	return b, nil
}

// corpusEntry is one record of a corpus file.
type corpusEntry struct {
	RecordID   string `json:"record_id"`
	Resource   string `json:"resource"`
	Text       string `json:"text"`
	ContextRef string `json:"context_ref"`
}

// ApplyAudit joins each decision with the Japanese and official English records it names and returns the
// canonical records. A decision whose key is missing on either side is an error: nothing is invented.
func ApplyAudit(b AuditBatch, jpPath, locPath string) ([]AuditedRecord, error) {
	jp, err := loadCorpusIndex(jpPath)
	if err != nil {
		return nil, err
	}
	loc, err := loadCorpusIndex(locPath)
	if err != nil {
		return nil, err
	}
	var out []AuditedRecord
	for _, d := range b.Decisions {
		id := d.Namespace + "\x00" + d.Key
		if _, ok := jp[id]; !ok {
			return nil, fmt.Errorf("%s/%s: not in the Japanese corpus", d.Namespace, d.Key)
		}
		l, ok := loc[id]
		if !ok {
			return nil, fmt.Errorf("%s/%s: not in the official English corpus", d.Namespace, d.Key)
		}
		text := l.Text
		if d.Text != "" {
			text = d.Text
		}
		if text == "" {
			return nil, fmt.Errorf("%s/%s: empty English text", d.Namespace, d.Key)
		}
		switch d.Status {
		case "KEEP", "CHANGE", "HOLD":
		default:
			return nil, fmt.Errorf("%s/%s: status %q is not one of KEEP, CHANGE, HOLD", d.Namespace, d.Key, d.Status)
		}
		if strings.TrimSpace(d.Reason) == "" {
			return nil, fmt.Errorf("%s/%s: a status needs a reason", d.Namespace, d.Key)
		}
		out = append(out, AuditedRecord{
			RecordID:          l.RecordID,
			Resource:          l.Resource,
			Text:              text,
			ContextRef:        "namespace=" + d.Namespace + " key=" + d.Key,
			Status:            d.Status,
			Reason:            d.Reason,
			Scene:             strings.TrimPrefix(d.Namespace, "ST_"),
			Speaker:           "",
			SpeakerConfidence: "none",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RecordID < out[j].RecordID })
	return out, nil
}

// loadCorpusIndex keys corpus records by (namespace, key). Records without a namespace are skipped: a key
// alone is not an identity and must never be joined on.
func loadCorpusIndex(path string) (map[string]corpusEntry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var records []corpusEntry
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("corpus %s: %w", path, err)
	}
	out := make(map[string]corpusEntry, len(records))
	for _, r := range records {
		ns := ""
		for _, f := range strings.Fields(r.ContextRef) {
			if strings.HasPrefix(f, "namespace=") {
				ns = strings.TrimPrefix(f, "namespace=")
			}
		}
		k := contextKey(r.ContextRef)
		if ns == "" || k == "" {
			continue
		}
		out[ns+"\x00"+k] = r
	}
	return out, nil
}

// WriteAuditedRecords merges the records into the canonical RealTL file: an existing record with the same
// record_id is replaced by the newer decision, everything else is preserved. A pass never drops work from
// an earlier pass.
func WriteAuditedRecords(path string, recs []AuditedRecord) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	merged := map[string]AuditedRecord{}
	var order []string
	if raw, err := os.ReadFile(path); err == nil {
		var existing []AuditedRecord
		if err := json.Unmarshal(raw, &existing); err == nil {
			for _, r := range existing {
				merged[r.RecordID] = r
				order = append(order, r.RecordID)
			}
		}
	}
	for _, r := range recs {
		if _, seen := merged[r.RecordID]; !seen {
			order = append(order, r.RecordID)
		}
		merged[r.RecordID] = r
	}
	sort.Strings(order)
	out := make([]AuditedRecord, 0, len(order))
	for _, id := range order {
		out = append(out, merged[id])
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
