package zen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"unicode"
)

// UntranslatedRecord names an aligned record whose English text is byte-identical to the Japanese and still
// contains Japanese script. Such a record is not a translation to audit: it is a gap that must be filled.
type UntranslatedRecord struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Text      string `json:"text"`
}

// UntranslatedReport is written to records/untranslated.json.
type UntranslatedReport struct {
	Source       string               `json:"source"`
	Aligned      int                  `json:"aligned_pairs"`
	Untranslated int                  `json:"untranslated"`
	Percent      float64              `json:"percent"`
	ByNamespace  []NamespaceCount     `json:"by_namespace"`
	Examples     []UntranslatedRecord `json:"examples"`
}

// NamespaceCount is one bucket of the untranslated census.
type NamespaceCount struct {
	Namespace string `json:"namespace"`
	Records   int    `json:"records"`
	Function  string `json:"function,omitempty"`
}

// HasJapaneseScript reports whether a string contains kana or CJK ideographs.
func HasJapaneseScript(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) {
			return true
		}
	}
	return false
}

// FindUntranslated compares the Japanese and English corpora record by record on (namespace, key) and
// reports the pairs whose English still carries the Japanese text. A key alone is never matched.
func FindUntranslated(jpPath, locPath string, dialogueNS map[string]bool) (UntranslatedReport, error) {
	jp, err := loadCorpusIndex(jpPath)
	if err != nil {
		return UntranslatedReport{}, err
	}
	loc, err := loadCorpusIndex(locPath)
	if err != nil {
		return UntranslatedReport{}, err
	}
	rep := UntranslatedReport{Source: filepath.Base(jpPath) + " vs " + filepath.Base(locPath)}
	counts := map[string]int{}
	for id, j := range jp {
		l, ok := loc[id]
		if !ok {
			continue
		}
		rep.Aligned++
		if j.Text == l.Text && HasJapaneseScript(l.Text) {
			rep.Untranslated++
			ns := id[:len(id)-len(contextKeyOf(id))-1]
			counts[ns]++
			if len(rep.Examples) < 20 {
				rep.Examples = append(rep.Examples, UntranslatedRecord{
					Namespace: ns, Key: contextKeyOf(id), Text: l.Text,
				})
			}
		}
	}
	if rep.Aligned > 0 {
		rep.Percent = float64(rep.Untranslated) / float64(rep.Aligned) * 100
	}
	for ns, n := range counts {
		fn, _ := FunctionFor(ns, dialogueNS)
		rep.ByNamespace = append(rep.ByNamespace, NamespaceCount{Namespace: ns, Records: n, Function: fn})
	}
	sort.Slice(rep.ByNamespace, func(i, j int) bool {
		if rep.ByNamespace[i].Records != rep.ByNamespace[j].Records {
			return rep.ByNamespace[i].Records > rep.ByNamespace[j].Records
		}
		return rep.ByNamespace[i].Namespace < rep.ByNamespace[j].Namespace
	})
	sort.Slice(rep.Examples, func(i, j int) bool {
		if rep.Examples[i].Namespace != rep.Examples[j].Namespace {
			return rep.Examples[i].Namespace < rep.Examples[j].Namespace
		}
		return rep.Examples[i].Key < rep.Examples[j].Key
	})
	return rep, nil
}

// contextKeyOf returns the key half of a "namespace\x00key" identity.
func contextKeyOf(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == 0 {
			return id[i+1:]
		}
	}
	return ""
}

// WriteUntranslatedReport writes the census.
func WriteUntranslatedReport(path string, rep UntranslatedReport) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
