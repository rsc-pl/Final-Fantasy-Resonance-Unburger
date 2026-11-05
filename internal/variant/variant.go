// Package variant loads and validates the optional text variants that sit on top of the canonical corpus.
//
// A variant file never holds a second corpus. It holds overrides: a record id, the hash of the canonical
// text.real_en the replacement was written against, and the full replacement line. Nothing here removes a suffix,
// rewrites a sentence or invents a translation.
package variant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"unburger/internal/corpuspack"
)

// SchemaVersion is the on-disk version of a variant file.
const SchemaVersion = 1

// NoHonorifics is the approved variant name.
const NoHonorifics = "no-honorifics"

// Override is one replacement line.
type Override struct {
	RecordID         string `json:"record_id"`
	BaseRealENSHA256 string `json:"base_real_en_sha256"`
	RealEN           string `json:"real_en"`
}

// File is translations/variants/<name>.json.
type File struct {
	SchemaVersion int        `json:"schema_version"`
	Variant       string     `json:"variant"`
	Base          string     `json:"base"`
	Overrides     []Override `json:"overrides"`
}

// Path returns the canonical location of a variant file.
func Path(dir, name string) string { return filepath.Join(dir, "variants", name+".json") }

// Load reads a variant file and refuses anything that is not the exact schema.
func Load(path string) (File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	var f File
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	if f.SchemaVersion != SchemaVersion {
		return File{}, fmt.Errorf("%s: schema version %d, want %d", path, f.SchemaVersion, SchemaVersion)
	}
	if f.Base != "canonical" {
		return File{}, fmt.Errorf("%s: base %q, want canonical", path, f.Base)
	}
	return f, nil
}

// Validate checks the overrides against the canonical corpus. Every rule here exists because the alternative
// would silently apply a stale or ambiguous line: an unknown id, a duplicate, a missing canonical translation or
// a stale hash is an error, never a warning.
func Validate(f File, idx *corpuspack.Index) ([]string, error) {
	var problems []string
	seen := map[string]bool{}
	for _, o := range f.Overrides {
		if o.RecordID == "" {
			problems = append(problems, "override without a record id")
			continue
		}
		if seen[o.RecordID] {
			problems = append(problems, "duplicate override for "+o.RecordID)
			continue
		}
		seen[o.RecordID] = true
		rec, ok := idx.ByID[o.RecordID]
		if !ok {
			problems = append(problems, "unknown record "+o.RecordID)
			continue
		}
		if rec.Text.RealEN == nil {
			problems = append(problems, "no approved canonical translation for "+o.RecordID)
			continue
		}
		if got := corpuspack.HashText(*rec.Text.RealEN); got != o.BaseRealENSHA256 {
			problems = append(problems, "canonical text changed since the override was written: "+o.RecordID)
			continue
		}
		if o.RealEN == "" {
			problems = append(problems, "override with an empty line for "+o.RecordID)
			continue
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return problems, fmt.Errorf("variant %s has %d invalid overrides", f.Variant, len(problems))
	}
	return nil, nil
}

// Apply returns the effective real translation per record id: the canonical line, replaced only where a
// validated override exists. A record the variant does not mention keeps exactly the canonical text.
func Apply(idx *corpuspack.Index, f *File) map[string]string {
	out := map[string]string{}
	for id, rec := range idx.ByID {
		if rec.Text.RealEN == nil {
			continue
		}
		out[id] = *rec.Text.RealEN
	}
	if f == nil {
		return out
	}
	for _, o := range f.Overrides {
		out[o.RecordID] = o.RealEN
	}
	return out
}

// Merge produces a new variant file from the existing one and an imported batch, refusing an override that
// would collide with a different decision.
func Merge(base File, incoming File) (File, error) {
	if incoming.Variant != base.Variant {
		return File{}, fmt.Errorf("variant mismatch: %q vs %q", incoming.Variant, base.Variant)
	}
	byID := map[string]Override{}
	order := []string{}
	for _, o := range base.Overrides {
		byID[o.RecordID] = o
		order = append(order, o.RecordID)
	}
	for _, o := range incoming.Overrides {
		if prev, ok := byID[o.RecordID]; ok {
			if prev.RealEN == o.RealEN && prev.BaseRealENSHA256 == o.BaseRealENSHA256 {
				continue
			}
			return File{}, fmt.Errorf("%s already carries a different override", o.RecordID)
		}
		byID[o.RecordID] = o
		order = append(order, o.RecordID)
	}
	sort.Strings(order)
	merged := File{SchemaVersion: SchemaVersion, Variant: base.Variant, Base: base.Base}
	for _, id := range order {
		merged.Overrides = append(merged.Overrides, byID[id])
	}
	return merged, nil
}

// Write saves a variant file atomically and deterministically.
func Write(path string, f File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	sort.Slice(f.Overrides, func(i, j int) bool { return f.Overrides[i].RecordID < f.Overrides[j].RecordID })
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
