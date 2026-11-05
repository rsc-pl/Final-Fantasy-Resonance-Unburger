// Package zen: character-name identity from the localisation corpus.
//
// The speaker stored on a dialogue row is a bare FName — a display label, not an identity. The corpus
// carries an independent, better-founded source: the ST_CharaName namespace, whose entries map a stable
// identifier (CharaName0034) to the Japanese character name shown on screen. Matching a speaker label
// against those entries by exact string equality therefore attaches an identifier to the label without
// inferring anything from similarity, table adjacency or co-occurrence.
//
// Two rules keep this honest. A label that matches more than one entry stays unresolved — the corpus has
// four such names, and picking one would be a guess. A label with no entry stays unresolved too; 空白 is
// the blank-label marker and correctly has no character behind it.
package zen

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// charaNamespace is the localisation namespace that holds character names.
const charaNamespace = "ST_CharaName"

// corpusRecord mirrors the fields of a record in translations/jp/records.json that this package reads.
type corpusRecord struct {
	Text       string `json:"text"`
	ContextRef string `json:"context_ref"`
}

// speakerFile is the part of records/speakers.json this package reads: the schema section's label
// histogram, which counts how many dialogue keys each speaker label carries.
type speakerFile struct {
	Schema struct {
		Labels map[string]int `json:"labels"`
	} `json:"schema"`
}

// SpeakerLabels reads the label histogram from records/speakers.json.
func SpeakerLabels(path string) (map[string]int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f speakerFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("speakers %s: %w", path, err)
	}
	if len(f.Schema.Labels) == 0 {
		return nil, fmt.Errorf("speakers %s: no schema labels", path)
	}
	return f.Schema.Labels, nil
}

// CharNameEntry is one entry of the character-name namespace.
type CharNameEntry struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// CharNameIndex resolves a display label to a character-name entry.
type CharNameIndex struct {
	byID    map[string]string
	byText  map[string][]string
	entries int
}

// LoadCharNames reads the Japanese corpus and indexes the ST_CharaName namespace.
func LoadCharNames(path string) (*CharNameIndex, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var records []corpusRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("corpus %s: %w", path, err)
	}
	idx := &CharNameIndex{byID: map[string]string{}, byText: map[string][]string{}}
	for _, r := range records {
		if !strings.Contains(r.ContextRef, "namespace="+charaNamespace) {
			continue
		}
		id := contextKey(r.ContextRef)
		if id == "" || r.Text == "" {
			continue
		}
		if _, seen := idx.byID[id]; seen {
			continue
		}
		idx.byID[id] = r.Text
		idx.byText[r.Text] = append(idx.byText[r.Text], id)
		idx.entries++
	}
	for text := range idx.byText {
		sort.Strings(idx.byText[text])
	}
	if idx.entries == 0 {
		return nil, fmt.Errorf("corpus %s: no %s entries", path, charaNamespace)
	}
	return idx, nil
}

// contextKey picks the key out of a context_ref such as "locres namespace=ST_CharaName key=CharaName0002".
func contextKey(ref string) string {
	i := strings.LastIndex(ref, "key=")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(ref[i+len("key="):])
}

// EntryCount is how many character-name entries the index holds.
func (x *CharNameIndex) EntryCount() int { return x.entries }

// Identity is the resolution of one speaker label.
type Identity struct {
	Label      string   `json:"label"`
	Keys       int      `json:"keys"`
	State      string   `json:"state"`
	ID         string   `json:"id,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	Text       string   `json:"text,omitempty"`
}

// Identity states. Only "unique" carries an identifier.
const (
	IdentityUnique    = "unique"
	IdentityAmbiguous = "ambiguous"
	IdentityMissing   = "missing"
)

// Resolve maps every label to at most one character-name entry.
func (x *CharNameIndex) Resolve(labels map[string]int) []Identity {
	out := make([]Identity, 0, len(labels))
	for label, keys := range labels {
		id := Identity{Label: label, Keys: keys}
		switch ids := x.byText[label]; len(ids) {
		case 0:
			id.State = IdentityMissing
		case 1:
			id.State = IdentityUnique
			id.ID = ids[0]
			id.Text = label
		default:
			id.State = IdentityAmbiguous
			id.Candidates = append([]string(nil), ids...)
		}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Keys != out[j].Keys {
			return out[i].Keys > out[j].Keys
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// IdentityReport is what gets written to records/character-names.json.
type IdentityReport struct {
	Source           string     `json:"source"`
	Namespace        string     `json:"namespace"`
	Entries          int        `json:"entries"`
	Labels           int        `json:"labels"`
	Unique           int        `json:"unique"`
	Ambiguous        int        `json:"ambiguous"`
	Missing          int        `json:"missing"`
	KeysTotal        int        `json:"keys_total"`
	KeysWithIdentity int        `json:"keys_with_identity"`
	CoveragePercent  float64    `json:"coverage_percent"`
	Rule             string     `json:"rule"`
	Identities       []Identity `json:"identities"`
}

// BuildIdentityReport resolves the labels and totals the result.
func BuildIdentityReport(idx *CharNameIndex, labels map[string]int) IdentityReport {
	rep := IdentityReport{
		Source:    "translations/jp/records.json#" + charaNamespace + " matched against records/speakers.json schema labels",
		Namespace: charaNamespace,
		Entries:   idx.EntryCount(),
		Rule: "a label takes an identifier only when the ST_CharaName entry text equals the label exactly " +
			"and exactly one entry carries that text; several candidates or none leave it unresolved",
	}
	rep.Identities = idx.Resolve(labels)
	rep.Labels = len(rep.Identities)
	for _, id := range rep.Identities {
		rep.KeysTotal += id.Keys
		switch id.State {
		case IdentityUnique:
			rep.Unique++
			rep.KeysWithIdentity += id.Keys
		case IdentityAmbiguous:
			rep.Ambiguous++
		default:
			rep.Missing++
		}
	}
	if rep.KeysTotal > 0 {
		rep.CoveragePercent = float64(rep.KeysWithIdentity) / float64(rep.KeysTotal) * 100
	}
	return rep
}

// WriteIdentityReport writes the report as indented JSON.
func WriteIdentityReport(path string, rep IdentityReport) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func dirOf(path string) string {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return "."
	}
	return path[:i]
}
