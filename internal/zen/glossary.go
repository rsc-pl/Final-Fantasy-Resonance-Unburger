package zen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GlossaryEntry is one term pair with the evidence that supports it. A pair is only written when records
// exist in which the Japanese term appears and the established English term appears in the English text of
// the same record; nothing is entered from a dictionary or from katakana resemblance.
type GlossaryEntry struct {
	JP       string   `json:"jp"`
	EN       string   `json:"en"`
	Field    string   `json:"field"`
	Status   string   `json:"status"`
	Evidence int      `json:"evidence_records"`
	Examples []string `json:"examples,omitempty"`
	Note     string   `json:"note,omitempty"`
}

// Glossary is the project term register.
type Glossary struct {
	Source      string          `json:"source"`
	Statuses    []string        `json:"status_values"`
	Entries     []GlossaryEntry `json:"entries"`
	Unresolved  []GlossaryEntry `json:"unresolved"`
	GeneratedBy string          `json:"generated_by"`
}

// GlossarySeed is a candidate pair. Field names the terminology area so a later pass can review one area at
// a time instead of re-reading every record.
type GlossarySeed struct {
	JP    string
	EN    string
	Field string
	Note  string
}

// GlossarySeeds lists the pairs observed in the audited families. They are candidates: the builder keeps only
// those the corpus itself supports.
var GlossarySeeds = []GlossarySeed{
	{"毒", "Poison", "status", ""},
	{"暗闇", "Blind", "status", ""},
	{"混乱", "Confuse", "status", ""},
	{"沈黙", "Silence", "status", ""},
	{"麻痺", "Paralysis", "status", ""},
	{"石化", "Petrify", "status", ""},
	{"睡眠", "Sleep", "status", ""},
	{"戦闘不能", "KO", "status", ""},
	{"火属性", "Fire", "element", ""},
	{"氷属性", "Ice", "element", ""},
	{"風属性", "Wind", "element", ""},
	{"土属性", "Earth", "element", ""},
	{"雷属性", "Lightning", "element", ""},
	{"水属性", "Water", "element", ""},
	{"無属性", "non-elemental", "element", ""},
	{"物理攻撃", "physical", "damage_type", ""},
	{"魔法攻撃", "magic", "damage_type", ""},
	{"敵全体", "all enemies", "scope", ""},
	{"敵単体", "an enemy", "scope", ""},
	{"味方全体", "all allies", "scope", ""},
	{"素早さ", "Speed", "stat", "JP is the agility stat; the English build calls it Speed. Needs a project decision before it is treated as settled."},
}

// BuildGlossary keeps a seed pair only when at least one record has the Japanese term in its JP text and
// the English term in the English text of that same record. A pair with evidence below the minimum goes to
// unresolved instead of the register, so nothing enters the glossary on a resemblance.
func BuildGlossary(jpPath, locPath string, minEvidence int) (Glossary, error) {
	jp, err := loadCorpusIndex(jpPath)
	if err != nil {
		return Glossary{}, err
	}
	loc, err := loadCorpusIndex(locPath)
	if err != nil {
		return Glossary{}, err
	}
	g := Glossary{
		Source:      filepath.Base(jpPath) + " vs " + filepath.Base(locPath),
		Statuses:    []string{"observed", "review_pending"},
		GeneratedBy: "unburger glossary",
	}
	for _, seed := range GlossarySeeds {
		e := GlossaryEntry{JP: seed.JP, EN: seed.EN, Field: seed.Field, Note: seed.Note, Status: "observed"}
		for id, j := range jp {
			l, ok := loc[id]
			if !ok {
				continue
			}
			if !strings.Contains(j.Text, seed.JP) || !strings.Contains(l.Text, seed.EN) {
				continue
			}
			e.Evidence++
			if len(e.Examples) < 3 {
				e.Examples = append(e.Examples, l.RecordID)
			}
		}
		sort.Strings(e.Examples)
		if seed.Note != "" {
			e.Status = "review_pending"
		}
		if e.Evidence < minEvidence {
			g.Unresolved = append(g.Unresolved, e)
			continue
		}
		g.Entries = append(g.Entries, e)
	}
	sort.Slice(g.Entries, func(i, j int) bool {
		if g.Entries[i].Field != g.Entries[j].Field {
			return g.Entries[i].Field < g.Entries[j].Field
		}
		return g.Entries[i].JP < g.Entries[j].JP
	})
	sort.Slice(g.Unresolved, func(i, j int) bool { return g.Unresolved[i].JP < g.Unresolved[j].JP })
	return g, nil
}

// WriteGlossary writes the register.
func WriteGlossary(path string, g Glossary) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
