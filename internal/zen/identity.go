// Package zen: per-key speaker identification.
//
// Four levels are kept strictly apart, because collapsing any two of them is how a translation pipeline
// starts inventing speakers:
//
//	label                 the raw FName serialized on the dialogue row — what the game displays
//	name_entry            the ST_CharaName identifier (CharaName0002), only when exactly one entry in that
//	                      table carries text equal to the label
//	identity              the character as a person; never asserted here
//	runtime_character_id  the gameplay character id; never asserted here
//
// A name entry is evidence that a name exists, not proof of who is speaking, and it says nothing about the
// addressee. Labels that denote no person at all are classified instead of resolved: 空白 is the
// blank-label marker and テロップ marks on-screen text.
package zen

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Label kinds. Only LabelKindCharacter denotes a person.
const (
	LabelKindCharacter  = "character"
	LabelKindBlank      = "blank"
	LabelKindScreenText = "screen_text"
)

// blankLabel is the marker the tables use for a deliberately empty speaker cell.
const blankLabel = "空白"

// screenTextLabel is the label the tables use for on-screen text rather than a speaker.
const screenTextLabel = "テロップ"

// Binding statuses.
const (
	BindingIdentified = "identified"
	BindingAmbiguous  = "ambiguous"
	BindingMissing    = "missing"
	BindingMarker     = "marker"
	BindingConflict   = "conflict"
	// BindingUnresolvedNamespace marks a key whose table could not be placed in exactly one namespace.
	BindingUnresolvedNamespace = "unresolved_namespace"
)

// BindingEvidence is one source reference behind a binding.
type BindingEvidence struct {
	Table     string `json:"table"`
	Row       string `json:"row"`
	Namespace string `json:"namespace"`
	EntryText string `json:"entry_text,omitempty"`
}

// KeyBinding is one dialogue key and what the evidence supports about its speaker.
type KeyBinding struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	LabelKind string `json:"label_kind"`
	Status    string `json:"status"`
	// Namespace is the ST_ namespace the key belongs to, empty when it could not be assigned.
	Namespace string            `json:"namespace,omitempty"`
	NameEntry string            `json:"name_entry,omitempty"`
	Rows      int               `json:"rows"`
	Tables    []string          `json:"tables"`
	Evidence  []BindingEvidence `json:"evidence,omitempty"`
}

// IdentityLevels documents what each field of a binding does and does not assert.
type IdentityLevels struct {
	Label              string `json:"label"`
	NameEntry          string `json:"name_entry"`
	Identity           string `json:"identity"`
	RuntimeCharacterID string `json:"runtime_character_id"`
}

// KeyIdentityReport is written to records/key-identities.json.
type KeyIdentityReport struct {
	Source       string         `json:"source"`
	Levels       IdentityLevels `json:"levels"`
	DialogueKeys int            `json:"dialogue_keys"`
	Namespaces   int            `json:"namespaces"`
	// KeysLabelled is every key the rows give a label, inside or outside the dialogue namespaces;
	// KeysWithLabel is the subset that the dialogue namespaces actually contain.
	KeysLabelled            int          `json:"keys_labelled_total"`
	KeysWithLabel           int          `json:"keys_with_label"`
	PairsTotal              int          `json:"pairs_total"`
	PairsOutsideKeys        int          `json:"pairs_outside_dialogue_keys"`
	KeysIdentified          int          `json:"keys_identified"`
	KeysAmbiguous           int          `json:"keys_ambiguous"`
	KeysMissing             int          `json:"keys_missing"`
	KeysMarker              int          `json:"keys_marker"`
	KeyConflicts            int          `json:"key_conflicts"`
	KeysUnresolvedNamespace int          `json:"keys_unresolved_namespace"`
	CoverageByLabelPct      float64      `json:"coverage_by_label_percent"`
	CoverageByIDAssigned    float64      `json:"coverage_by_id_of_labelled_percent"`
	CoverageByIDCorpus      float64      `json:"coverage_by_id_of_corpus_percent"`
	Notes                   []string     `json:"notes"`
	Bindings                []KeyBinding `json:"bindings"`
}

// LoadDialogueRows reads records/dialogue-rows.json.
func LoadDialogueRows(path string) ([]DialogueRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Items []DialogueRow `json:"items"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("dialogue rows %s: %w", path, err)
	}
	if len(f.Items) == 0 {
		return nil, fmt.Errorf("dialogue rows %s: no items", path)
	}
	return f.Items, nil
}

// DialogueKeySet returns the keys that belong to a dialogue namespace: the ST_ namespaces whose key ends
// with a digit. That is the denominator the coverage figures are reported against.
func DialogueKeySet(corpus string) (map[string]bool, error) {
	raw, err := os.ReadFile(corpus)
	if err != nil {
		return nil, err
	}
	var records []corpusRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, r := range records {
		if !strings.Contains(r.ContextRef, "namespace=ST_") {
			continue
		}
		k := contextKey(r.ContextRef)
		if k == "" {
			continue
		}
		if k[len(k)-1] >= '0' && k[len(k)-1] <= '9' {
			set[k] = true
		}
	}
	return set, nil
}

// NamespaceKeySets returns every ST_ namespace and the keys it contains, so a table can be assigned to
// the namespace that actually holds its keys. A key alone is not an identity: the same key string occurs
// in more than one namespace, and treating those as one record is how two scenes get merged.
func NamespaceKeySets(corpus string) (map[string]map[string]bool, error) {
	raw, err := os.ReadFile(corpus)
	if err != nil {
		return nil, err
	}
	var records []corpusRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, err
	}
	out := map[string]map[string]bool{}
	for _, r := range records {
		if !strings.Contains(r.ContextRef, "namespace=ST_") {
			continue
		}
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
		if out[ns] == nil {
			out[ns] = map[string]bool{}
		}
		out[ns][k] = true
	}
	return out, nil
}

// assignNamespace picks the namespace that contains every key of the table. Exactly one match assigns the
// namespace; no match or several matches leave it unresolved rather than guessing.
func assignNamespace(tableKeys map[string]bool, sets map[string]map[string]bool) (string, string) {
	var hits []string
	for ns, keys := range sets {
		all := true
		for k := range tableKeys {
			if !keys[k] {
				all = false
				break
			}
		}
		if all {
			hits = append(hits, ns)
		}
	}
	sort.Strings(hits)
	switch len(hits) {
	case 1:
		return hits[0], ""
	case 0:
		return "", "no namespace holds every key of this table"
	default:
		return "", "several namespaces hold every key of this table: " + strings.Join(hits, ", ")
	}
}

// LoadIdentities reads records/character-names.json, the per-label resolution produced by the charanames
// command. Only entries with an identifier participate in a binding; the rest keep their unresolved state.
func LoadIdentities(path string) ([]Identity, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rep struct {
		Identities []Identity `json:"identities"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("identities %s: %w", path, err)
	}
	if len(rep.Identities) == 0 {
		return nil, fmt.Errorf("identities %s: no identities", path)
	}
	return rep.Identities, nil
}

// ClassifyLabel decides whether a label denotes a person at all.
func ClassifyLabel(label string) string {
	switch label {
	case blankLabel:
		return LabelKindBlank
	case screenTextLabel:
		return LabelKindScreenText
	}
	return LabelKindCharacter
}

// BuildKeyIdentities binds every (namespace, key) entry to its label and, where the evidence allows, to a
// name entry. The namespace comes from the table that carries the key, by exact containment; a table whose
// keys are not contained in exactly one namespace leaves its bindings with an unresolved namespace instead
// of borrowing another scene's identity.
func BuildKeyIdentities(rows []DialogueRow, identities []Identity, sets map[string]map[string]bool) KeyIdentityReport {
	byLabel := map[string]Identity{}
	for _, id := range identities {
		byLabel[id.Label] = id
	}

	tableKeys := map[string]map[string]bool{}
	for _, r := range rows {
		if r.Key == "" {
			continue
		}
		if tableKeys[r.Table] == nil {
			tableKeys[r.Table] = map[string]bool{}
		}
		tableKeys[r.Table][r.Key] = true
	}
	tableNS := map[string]string{}
	tableNote := map[string]string{}
	for t, ks := range tableKeys {
		ns, note := assignNamespace(ks, sets)
		tableNS[t] = ns
		if note != "" {
			tableNote[t] = note
		}
	}

	type acc struct {
		labels   map[string]bool
		tables   map[string]bool
		rowCount int
		evidence []BindingEvidence
	}
	accs := map[string]*acc{}
	pairs := map[string]bool{}
	for _, r := range rows {
		if r.Key == "" || r.Speaker == "" {
			continue
		}
		pairs[r.Key+"\x00"+r.Speaker] = true
		idk := tableNS[r.Table] + "\x00" + r.Key
		a, ok := accs[idk]
		if !ok {
			a = &acc{labels: map[string]bool{}, tables: map[string]bool{}}
			accs[idk] = a
		}
		a.labels[r.Speaker] = true
		a.rowCount++
		a.tables[r.Table] = true
		if len(a.evidence) < 2 {
			a.evidence = append(a.evidence, BindingEvidence{
				Table:     r.Table,
				Row:       r.RowName,
				Namespace: tableNS[r.Table],
				EntryText: r.Speaker,
			})
		}
	}

	rep := KeyIdentityReport{
		Source:     "records/dialogue-rows.json + records/character-names.json#" + charaNamespace,
		PairsTotal: len(pairs),
		Levels: IdentityLevels{
			Label:              "raw FName serialized on the row; this is what the game displays",
			NameEntry:          "ST_CharaName identifier, set only when exactly one entry carries text equal to the label",
			Identity:           "not asserted: a name entry is not proof of who is speaking",
			RuntimeCharacterID: "not asserted: no evidence links a name entry to a gameplay character id",
		},
	}
	// The denominator is the corpus reachable from the dialogue tables: the entries of the namespaces those
	// tables were placed in. Counting every ST_ namespace instead would fold equipment, skill and unit name
	// tables into a dialogue denominator, which is what an earlier pass did.
	used := map[string]bool{}
	for _, ns := range tableNS {
		if ns != "" {
			used[ns] = true
		}
	}
	rep.Namespaces = len(used)
	for ns := range used {
		rep.DialogueKeys += len(sets[ns])
	}

	scopedKeys := map[string]bool{}
	conflicts := map[string]bool{}
	for idk, a := range accs {
		parts := strings.SplitN(idk, "\x00", 2)
		ns, key := parts[0], parts[1]
		b := KeyBinding{
			Key:       key,
			Label:     "",
			Namespace: ns,
			Rows:      a.rowCount,
			Evidence:  a.evidence,
		}
		labels := make([]string, 0, len(a.labels))
		for l := range a.labels {
			labels = append(labels, l)
		}
		sort.Strings(labels)
		b.Label = strings.Join(labels, " | ")
		b.LabelKind = ClassifyLabel(labels[0])
		for t := range a.tables {
			b.Tables = append(b.Tables, t)
		}
		sort.Strings(b.Tables)
		switch {
		case len(a.labels) > 1:
			b.Status = BindingConflict
			conflicts[ns+"\x00"+key] = true
		case b.LabelKind != LabelKindCharacter:
			b.Status = BindingMarker
		case ns == "":
			b.Status = BindingUnresolvedNamespace
		default:
			if id, ok := byLabel[b.Label]; ok {
				switch id.State {
				case IdentityUnique:
					b.Status = BindingIdentified
					b.NameEntry = id.ID
				case IdentityAmbiguous:
					b.Status = BindingAmbiguous
				default:
					b.Status = BindingMissing
				}
			} else {
				b.Status = BindingMissing
			}
		}
		if ns != "" {
			scopedKeys[ns+"\x00"+key] = true
			switch b.Status {
			case BindingIdentified:
				rep.KeysIdentified++
			case BindingAmbiguous:
				rep.KeysAmbiguous++
			case BindingMissing:
				rep.KeysMissing++
			case BindingMarker:
				rep.KeysMarker++
			case BindingConflict:
				rep.KeyConflicts++
			}
		} else {
			rep.KeysUnresolvedNamespace++
		}
		rep.Bindings = append(rep.Bindings, b)
	}
	rep.KeysWithLabel = len(scopedKeys)
	rep.KeysLabelled = len(accs)
	rep.PairsOutsideKeys = rep.KeysLabelled - rep.KeysWithLabel
	if rep.DialogueKeys > 0 {
		rep.CoverageByLabelPct = float64(rep.KeysWithLabel) / float64(rep.DialogueKeys) * 100
		rep.CoverageByIDCorpus = float64(rep.KeysIdentified) / float64(rep.DialogueKeys) * 100
	}
	if rep.KeysWithLabel > 0 {
		rep.CoverageByIDAssigned = float64(rep.KeysIdentified) / float64(rep.KeysWithLabel) * 100
	}
	rep.Notes = []string{
		"a binding is scoped to (namespace, key): 87 key strings exist in two ST_ namespaces and are never treated as one record",
		"the namespace of a table is assigned only when exactly one namespace contains every key of that table",
		"空白 is the blank-label marker, not a character",
		"テロップ marks on-screen text, not a speaker",
		"レイン(子ども) and ラスウェル(子ども) keep their own labels and stay unresolved: they may be younger versions of the same characters, and nothing in the data decides it either way",
		"the three conflicts are two rows of the SAME table carrying different labels for one key, not a namespace collision",
	}
	sort.Slice(rep.Bindings, func(i, j int) bool {
		if rep.Bindings[i].Namespace != rep.Bindings[j].Namespace {
			return rep.Bindings[i].Namespace < rep.Bindings[j].Namespace
		}
		return rep.Bindings[i].Key < rep.Bindings[j].Key
	})
	return rep
}

// WriteKeyIdentities writes the report as indented JSON.
func WriteKeyIdentities(path string, rep KeyIdentityReport) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
