package zen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// StatusUnreviewed marks a record that carries no recorded review. It is the only status a record may hold until
// a review is recorded; KEEP, CHANGE and HOLD require that record.
const StatusUnreviewed = "UNREVIEWED"

// Assessment is one editorial status found in the real-translation record, together with where it came from.
// A status is only an assessment if something recorded that it was made and approved; otherwise it is an
// unreviewed claim and must not be reported as a decision.
type Assessment struct {
	RecordID   string `json:"record_id"`
	ContextRef string `json:"context_ref,omitempty"`
	Text       string `json:"text"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	// Origin names the file or pass that produced the status. An assessment whose origin is a batch of
	// automated suggestions has no approval behind it.
	Origin string `json:"origin"`
	// Approved is true only when the source of the status carries an explicit approval record.
	Approved   bool   `json:"approved"`
	ApprovalBy string `json:"approved_by,omitempty"`
}

// ProvenanceReport answers, in numbers, how many editorial statuses exist, where each came from, and how
// many of them can point at a recorded review.
type ProvenanceReport struct {
	Source            string         `json:"source"`
	Records           int            `json:"records"`
	ByStatus          map[string]int `json:"by_status"`
	ByOrigin          map[string]int `json:"by_origin"`
	Approved          int            `json:"approved"`
	Unapproved        int            `json:"unapproved"`
	WithReason        int            `json:"with_reason"`
	WithoutReason     int            `json:"without_reason"`
	ReviewedStatuses  int            `json:"records_with_editorial_status"`
	UnreviewedRecords int            `json:"records_already_unreviewed"`
	Notes             []string       `json:"notes"`
}

// batchFile is one saved batch of decisions.
type batchFile struct {
	Pass      string `json:"pass"`
	Family    string `json:"family"`
	Decisions []struct {
		Namespace string `json:"namespace"`
		Key       string `json:"key"`
		Status    string `json:"status"`
		Reason    string `json:"reason"`
		Text      string `json:"text"`
	} `json:"decisions"`
	// Approval names who signed the batch and when. Empty means no sign-off is recorded for that batch.
	Approval struct {
		ApprovedBy string `json:"approved_by"`
		ApprovedAt string `json:"approved_at"`
	} `json:"approval"`
}

// BuildProvenance reads the real-translation record and attributes every status to the batch that produced
// it. Nothing is inferred: a status with no approval record is counted as unapproved.
func BuildProvenance(enPath, batchDir string) (ProvenanceReport, []Assessment, error) {
	raw, err := os.ReadFile(enPath)
	if err != nil {
		return ProvenanceReport{}, nil, err
	}
	var recs []AuditedRecord
	if err := json.Unmarshal(raw, &recs); err != nil {
		return ProvenanceReport{}, nil, fmt.Errorf("%s: %w", enPath, err)
	}
	origins, approvals := loadBatchOrigins(batchDir)
	rep := ProvenanceReport{
		Source:   filepath.Base(enPath) + " + " + batchDir,
		ByStatus: map[string]int{},
		ByOrigin: map[string]int{},
	}
	out := make([]Assessment, 0, len(recs))
	for _, r := range recs {
		origin, ok := origins[originKey(r.ContextRef)]
		if !ok {
			origin = "unknown"
		}
		approved := approvals[origin]
		rep.Records++
		rep.ByStatus[r.Status]++
		rep.ByOrigin[origin]++
		if r.Status == StatusUnreviewed {
			rep.UnreviewedRecords++
		} else {
			rep.ReviewedStatuses++
		}
		if approved {
			rep.Approved++
		} else if r.Status != StatusUnreviewed {
			rep.Unapproved++
		}
		if strings.TrimSpace(r.Reason) != "" {
			rep.WithReason++
		} else {
			rep.WithoutReason++
		}
		out = append(out, Assessment{
			RecordID:   r.RecordID,
			ContextRef: r.ContextRef,
			Text:       r.Text,
			Status:     r.Status,
			Reason:     r.Reason,
			Origin:     origin,
			Approved:   approved,
		})
	}
	rep.Notes = []string{
		"a status is an editorial claim only when a recorded approval names who approved it and when",
		fmt.Sprintf("%d batch file(s) under the batch directory carry an approval record", len(approvals)),
		"a status without a recorded approval is an unreviewed claim, not a decision, whatever its value reads",
		"the reason text proves that a claim exists, never that the Japanese was checked or the decision approved",
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RecordID < out[j].RecordID })
	return rep, out, nil
}

// originKey turns a context reference into the (namespace, key) join the batches use.
func originKey(ctx string) string {
	ns, k := "", ""
	for _, f := range strings.Fields(ctx) {
		if strings.HasPrefix(f, "namespace=") {
			ns = strings.TrimPrefix(f, "namespace=")
		}
		if strings.HasPrefix(f, "key=") {
			k = strings.TrimPrefix(f, "key=")
		}
	}
	if ns == "" || k == "" {
		return ""
	}
	return ns + "\x00" + k
}

// loadBatchOrigins maps (namespace, key) to the batch file that decided it, and reports which batches carry
// an approval record.
func loadBatchOrigins(dir string) (map[string]string, map[string]bool) {
	origins := map[string]string{}
	approved := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return origins, approved
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var b batchFile
		if err := json.Unmarshal(raw, &b); err != nil {
			continue
		}
		label := name
		if b.Pass != "" {
			label = "pass-" + b.Pass + ":" + name
		}
		if b.Approval.ApprovedBy != "" {
			approved[label] = true
		}
		for _, d := range b.Decisions {
			origins[d.Namespace+"\x00"+d.Key] = label
		}
	}
	return origins, approved
}

// MigrateToUnreviewed rewrites the real-translation record so that every record without a recorded approval
// is marked UNREVIEWED and carries the official English text unchanged. The previous assessments are written
// to the private history path first, so no claim is lost and no official text is altered.
func MigrateToUnreviewed(enPath, officialPath, historyPath, outPath string) (ProvenanceReport, error) {
	rep, assessments, err := BuildProvenance(enPath, filepath.Join(".local", "toolchain", "tests"))
	if err != nil {
		return rep, err
	}
	if err := writeJSON(historyPath, assessments); err != nil {
		return rep, err
	}
	official, err := loadCorpusIndex(officialPath)
	if err != nil {
		return rep, err
	}
	var out []AuditedRecord
	for _, a := range assessments {
		if a.Approved {
			out = append(out, AuditedRecord{
				RecordID: a.RecordID, Text: a.Text, ContextRef: a.ContextRef,
				Status: a.Status, Reason: a.Reason, SpeakerConfidence: "none",
			})
			continue
		}
		o, ok := official[originKey(a.ContextRef)]
		if !ok {
			return rep, fmt.Errorf("%s: no official English record for %s", a.RecordID, a.ContextRef)
		}
		out = append(out, AuditedRecord{
			RecordID:          a.RecordID,
			Resource:          o.Resource,
			Text:              o.Text,
			ContextRef:        a.ContextRef,
			Status:            StatusUnreviewed,
			Reason:            "",
			SpeakerConfidence: "none",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RecordID < out[j].RecordID })
	return rep, writeJSON(outPath, out)
}

func writeJSON(path string, v interface{}) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
