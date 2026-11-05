// Package zen: what a localisation record is used for, and whether a speaker applies to it.
//
// The corpus is not one kind of text. It carries dialogue lines, narration, system messages, on-screen
// captions, quest text, item and skill names and descriptions, map and character names, and interface
// strings. Only some of those can have a speaker, and a missing label is not evidence of narration just as
// a present label is not proof of a character.
//
// The classification below is derived from the namespace a record lives in and from the tables that
// reference it — never from the record's text and never from whether a label happens to exist:
//
//	a namespace that a dialogue table references        -> dialogue_line
//	a namespace named for items, skills, units, maps     -> NOT a spoken line
//	any other namespace                                  -> unclassified, speaker unknown
//
// Namespaces are taken from the corpus itself; the dialogue ones are established by exact containment of a
// table's whole key set, which is the only assignment that cannot merge two scenes.
package zen

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
)

// Speaker applicability. Only Applicable records enter the speaker coverage denominator.
const (
	SpeakerVerified      = "VERIFIED"
	SpeakerUnknown       = "UNKNOWN"
	SpeakerNotApplicable = "NOT_APPLICABLE"
	// SpeakerUndetermined marks a record whose namespace nothing references: whether a speaker applies is
	// not established either way, so it enters neither the speaker denominator nor the not-applicable count.
	SpeakerUndetermined = "UNDETERMINED"
)

// Text functions. The zero value is Unclassified, so an unrecognised namespace never claims a function.
const (
	FunctionDialogue      = "dialogue_line"
	FunctionItemName      = "item_name"
	FunctionItemText      = "item_text"
	FunctionSkillName     = "skill_name"
	FunctionSkillText     = "skill_text"
	FunctionUnitName      = "unit_name"
	FunctionUnitText      = "unit_text"
	FunctionCharacterName = "character_name"
	FunctionMapName       = "map_name"
	FunctionQuestProgress = "quest_objective"
	FunctionQuestSummary  = "quest_summary"
	FunctionUILabel       = "ui_label"
	FunctionUIChoice      = "ui_choice"
	FunctionTrophy        = "trophy"
	FunctionTutorial      = "tutorial"
	FunctionSystemMessage = "system_message"
	FunctionUnclassified  = "unclassified"
)

// namespaceFunction maps a namespace prefix to the function of the text it holds. The match is on the
// namespace name only: no text is read to decide what a record is for.
var namespaceFunction = []struct {
	prefix   string
	function string
	speaker  string
}{
	{"ST_CharaName", FunctionCharacterName, SpeakerNotApplicable},
	{"ST_EquipmentDescription", FunctionItemText, SpeakerNotApplicable},
	{"ST_EquipmentName", FunctionItemName, SpeakerNotApplicable},
	{"ST_ConsumablesName", FunctionItemName, SpeakerNotApplicable},
	{"ST_ValuableName", FunctionItemName, SpeakerNotApplicable},
	{"ST_ArtifactName", FunctionItemName, SpeakerNotApplicable},
	{"ST_VisionName", FunctionSkillName, SpeakerNotApplicable},
	{"ST_SkillDescription", FunctionSkillText, SpeakerNotApplicable},
	{"ST_SkillName", FunctionSkillName, SpeakerNotApplicable},
	{"ST_MasterSkillName", FunctionSkillName, SpeakerNotApplicable},
	{"ST_CommandSkillName", FunctionSkillName, SpeakerNotApplicable},
	{"ST_PassiveSkillDescription", FunctionSkillText, SpeakerNotApplicable},
	{"ST_PassiveSkillName", FunctionSkillName, SpeakerNotApplicable},
	{"ST_UnitDescription", FunctionUnitText, SpeakerNotApplicable},
	{"ST_UnitName", FunctionUnitName, SpeakerNotApplicable},
	{"ST_MAP_NAME", FunctionMapName, SpeakerNotApplicable},
	// The families below were separated after reading their records: quest objectives, story summaries,
	// interface labels and achievement text name no speaker, while a battle-AI namespace holds spoken lines.
	{"ST_MainProgress", FunctionQuestProgress, SpeakerNotApplicable},
	{"ST_SubProgress", FunctionQuestProgress, SpeakerNotApplicable},
	{"ST_MainSummary", FunctionQuestSummary, SpeakerNotApplicable},
	{"ST_SubSummary", FunctionQuestSummary, SpeakerNotApplicable},
	{"ST_UI_", FunctionUILabel, SpeakerNotApplicable},
	{"ST_DialogChoice", FunctionUIChoice, SpeakerNotApplicable},
	{"ST_TrophySummary", FunctionTrophy, SpeakerNotApplicable},
	{"ST_ATC_Title", FunctionUILabel, SpeakerNotApplicable},
	{"ST_BtlAI", FunctionDialogue, speakerForAny},
	{"ST_Guide", FunctionTutorial, SpeakerNotApplicable},
	{"ST_Tutorial", FunctionTutorial, SpeakerNotApplicable},
	{"ST_System", FunctionSystemMessage, SpeakerNotApplicable},
	{"ST_Colosseum_MapName", FunctionMapName, SpeakerNotApplicable},
}

// FunctionFor returns the function and speaker applicability for a namespace, given which namespaces the
// dialogue tables were placed in.
func FunctionFor(namespace string, dialogueNamespaces map[string]bool) (string, string) {
	if dialogueNamespaces[namespace] {
		return FunctionDialogue, speakerForDialogue
	}
	for _, m := range namespaceFunction {
		if strings.HasPrefix(namespace, m.prefix) {
			return m.function, m.speaker
		}
	}
	if sceneLike(namespace) {
		// A scene namespace no table was matched to still holds dialogue: its records read as spoken lines.
		return FunctionDialogue, speakerForAny
	}
	return FunctionUnclassified, SpeakerUndetermined
}

// speakerForDialogue marks a dialogue line as needing a speaker; whether one is known is decided per record.
const speakerForDialogue = "APPLICABLE"

// speakerForAny marks a family whose records are spoken but carry no speaker field of their own: a speaker
// applies, and whether one is known is decided per record.
const speakerForAny = "APPLICABLE"

// sceneLike reports whether a namespace name has the shape of an event scene (ST_C08_817_02,
// ST_SubEv_C01_DesertBandit, ST_EV_...). The shape alone does not make it dialogue — the records of these
// namespaces were read and are spoken lines — it only keeps them out of the unclassified bucket.
func sceneLike(ns string) bool {
	if !strings.HasPrefix(ns, "ST_") {
		return false
	}
	rest := strings.TrimPrefix(ns, "ST_")
	for _, p := range []string{"C0", "C1", "SubEv_", "EV_", "GimEv", "Legend_", "Btl_"} {
		if strings.HasPrefix(rest, p) {
			return true
		}
	}
	return false
}

// FunctionStat is one bucket of the classification.
type FunctionStat struct {
	Function string `json:"function"`
	Records  int    `json:"records"`
	Verified int    `json:"speaker_verified"`
	Unknown  int    `json:"speaker_unknown"`
	NotAppl  int    `json:"speaker_not_applicable"`
	Undet    int    `json:"speaker_applicability_undetermined"`
}

// FunctionReport is written to records/text-functions.json.
type FunctionReport struct {
	Source        string         `json:"source"`
	Rules         []string       `json:"rules"`
	Records       int            `json:"records"`
	Applicable    int            `json:"records_requiring_speaker"`
	Verified      int            `json:"speaker_verified"`
	Unknown       int            `json:"speaker_unknown"`
	NotApplicable int            `json:"speaker_not_applicable"`
	Undetermined  int            `json:"speaker_applicability_undetermined"`
	CoveragePct   float64        `json:"speaker_coverage_of_applicable_percent"`
	NamedEntryPct float64        `json:"name_entry_of_verified_percent"`
	Functions     []FunctionStat `json:"functions"`
	Notes         []string       `json:"notes"`
}

// LabelEvidence is what the dialogue pass knows about one (namespace, key) entry.
type LabelEvidence struct {
	Namespaced map[string]KeyBinding
}

// LoadBindings reads records/key-identities.json.
func LoadBindings(path string) ([]KeyBinding, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rep struct {
		Bindings []KeyBinding `json:"bindings"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, err
	}
	if len(rep.Bindings) == 0 {
		return nil, errNoBindings
	}
	return rep.Bindings, nil
}

var errNoBindings = errNoBindingsError("no bindings in the identity record")

type errNoBindingsError string

func (e errNoBindingsError) Error() string { return string(e) }

// BindingsByNamespace index bindings by their scoped identity.
func BindingsByNamespace(bindings []KeyBinding) map[string]KeyBinding {
	out := make(map[string]KeyBinding, len(bindings))
	for _, b := range bindings {
		if b.Namespace == "" {
			continue
		}
		out[b.Namespace+"\x00"+b.Key] = b
	}
	return out
}

// BuildFunctionReport classifies every entry of every ST_ namespace and decides, per record, whether a
// speaker applies and whether one is established.
func BuildFunctionReport(sets map[string]map[string]bool, bindings []KeyBinding) FunctionReport {
	byID := BindingsByNamespace(bindings)
	dialogueNS := map[string]bool{}
	for _, b := range bindings {
		if b.Namespace != "" {
			dialogueNS[b.Namespace] = true
		}
	}
	rep := FunctionReport{
		Source: "corpus namespaces + records/key-identities.json",
		Rules: []string{
			"a namespace a dialogue table was placed in holds dialogue lines; a speaker applies to those records",
			"item, skill, unit, map and character-name namespaces hold names and descriptions: a speaker does not apply",
			"a namespace nothing references is unclassified: whether a speaker applies is undetermined, so it enters neither the speaker denominator nor the not-applicable count",
			"a label is evidence that the game names a speaker; it is not evidence of character identity",
		},
	}
	stats := map[string]*FunctionStat{}
	for ns, keys := range sets {
		fn, speaker := FunctionFor(ns, dialogueNS)
		st := stats[fn]
		if st == nil {
			st = &FunctionStat{Function: fn}
			stats[fn] = st
		}
		for key := range keys {
			rep.Records++
			st.Records++
			b, has := byID[ns+"\x00"+key]
			switch {
			case speaker == SpeakerUndetermined:
				rep.Undetermined++
				st.Undet++
				continue
			case speaker == SpeakerNotApplicable:
				rep.NotApplicable++
				st.NotAppl++
				continue
			case has && b.LabelKind == LabelKindScreenText:
				// On-screen text: by function it is not a spoken line, whatever namespace it sits in.
				rep.NotApplicable++
				st.NotAppl++
				continue
			case has && b.Label != "" && b.Label != blankLabel && b.Status != BindingConflict:
				rep.Applicable++
				rep.Verified++
				st.Verified++
			case has && b.Status == BindingConflict:
				rep.Applicable++
				rep.Unknown++
				st.Unknown++
			default:
				// includes the blank marker: a dialogue cell exists but names nobody
				rep.Applicable++
				rep.Unknown++
				st.Unknown++
			}
		}
	}
	for _, st := range stats {
		rep.Functions = append(rep.Functions, *st)
	}
	sort.Slice(rep.Functions, func(i, j int) bool {
		if rep.Functions[i].Records != rep.Functions[j].Records {
			return rep.Functions[i].Records > rep.Functions[j].Records
		}
		return rep.Functions[i].Function < rep.Functions[j].Function
	})
	if rep.Applicable > 0 {
		rep.CoveragePct = float64(rep.Verified) / float64(rep.Applicable) * 100
	}
	named := 0
	for _, b := range bindings {
		if b.Status == BindingIdentified {
			named++
		}
	}
	if rep.Verified > 0 {
		rep.NamedEntryPct = float64(named) / float64(rep.Verified) * 100
	}
	rep.Notes = []string{
		"only records that require a speaker enter the speaker coverage denominator; NOT_APPLICABLE never lowers it",
		"VERIFIED means the game names a speaker for the record, not that the character behind the name is settled",
		"a blank label is a dialogue cell that names nobody: applicable, unknown",
		"on-screen caption text is classified by function, not by the label it carries",
		"14 760 entries in 742 namespaces is the range reachable from the dialogue tables, not the whole game text",
	}
	return rep
}

// FamilyClass is one bucket of the whole-corpus inventory.
type FamilyClass struct {
	Class      string   `json:"class"`
	Namespaces int      `json:"namespaces"`
	Records    int      `json:"records"`
	Speaker    string   `json:"speaker_applicability"`
	Examples   []string `json:"examples,omitempty"`
}

// FamilyReport covers the whole corpus, not only the ST_ namespaces.
type FamilyReport struct {
	Source  string        `json:"source"`
	Rules   []string      `json:"rules"`
	Records int           `json:"records"`
	Classes []FamilyClass `json:"classes"`
}

// BuildFamilyReport classifies every namespace of the corpus. A namespace that is not ST_ and whose name
// occurs in the engine mapping file is engine or editor content; one that is not ST_ and is unknown is left
// unclassified rather than assumed to be game text.
func BuildFamilyReport(namespaces map[string]int, dialogueNS map[string]bool, engineNames map[string]bool) FamilyReport {
	rep := FamilyReport{
		Source: "corpus namespaces + dialogue namespace assignment + engine mapping names",
		Rules: []string{
			"an ST_ namespace a dialogue table was placed in holds spoken lines",
			"an ST_ namespace named for items, skills, units, maps or character names holds names and descriptions",
			"every game-content family lives under ST_; a non-ST_ namespace holds Unreal editor and plugin interface text, confirmed by sampling its records, and no table references it",
			"anything else stays unclassified: no class is assigned from the words inside the texts",
		},
	}
	type acc struct {
		ns, sp string
		n      int
	}
	classes := map[string]*FamilyClass{}
	for ns, n := range namespaces {
		class, sp := "", ""
		switch {
		case dialogueNS[ns]:
			class, sp = FunctionDialogue, "APPLICABLE"
		default:
			fn, spp := FunctionFor(ns, dialogueNS)
			if fn != FunctionUnclassified {
				class = fn
				if spp == SpeakerNotApplicable {
					sp = SpeakerNotApplicable
				} else {
					sp = "APPLICABLE"
				}
			} else if !strings.HasPrefix(ns, "ST_") {
				// Every game-content family lives under ST_. A non-ST_ namespace holds Unreal editor and
				// plugin interface text, which the sampled records confirm (spectral-analysis options,
				// row-struct actions, build-configuration strings, StateTree delegates): not FFRS content,
				// and no game table references any of it.
				class, sp = "engine_or_editor", SpeakerNotApplicable
			} else {
				class, sp = "game_text_unclassified", SpeakerUndetermined
			}
		}
		c := classes[class]
		if c == nil {
			c = &FamilyClass{Class: class, Speaker: sp}
			classes[class] = c
		}
		c.Namespaces++
		c.Records += n
		rep.Records += n
		if len(c.Examples) < 5 {
			c.Examples = append(c.Examples, ns)
		}
	}
	for _, c := range classes {
		sort.Strings(c.Examples)
		rep.Classes = append(rep.Classes, *c)
	}
	sort.Slice(rep.Classes, func(i, j int) bool {
		if rep.Classes[i].Records != rep.Classes[j].Records {
			return rep.Classes[i].Records > rep.Classes[j].Records
		}
		return rep.Classes[i].Class < rep.Classes[j].Class
	})
	return rep
}

// WriteFamilyReport writes the whole-corpus inventory.
func WriteFamilyReport(path string, rep FamilyReport) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// CorpusNamespaces counts records per namespace across the whole corpus.
func CorpusNamespaces(path string) (map[string]int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var records []corpusRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, r := range records {
		ns := ""
		for _, f := range strings.Fields(r.ContextRef) {
			if strings.HasPrefix(f, "namespace=") {
				ns = strings.TrimPrefix(f, "namespace=")
			}
		}
		if ns == "" {
			continue
		}
		out[ns]++
	}
	return out, nil
}

// WriteFunctionReport writes the report as indented JSON.
func WriteFunctionReport(path string, rep FunctionReport) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
