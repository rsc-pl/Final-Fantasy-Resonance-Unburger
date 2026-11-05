// Package corpuspack is the canonical, split corpus. One file per source group holds the three texts side by
// side — the Japanese original, the official English and our real translation — together with the context that
// was proved for it. Nothing else is a source of truth: the toolchain reads this package, never a monolith.
package corpuspack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SchemaVersion is the on-disk format version. A reader must refuse a file it does not know.
const SchemaVersion = 1

// FormatName declares the active corpus format in the manifest.
const FormatName = "canonical-split-v1"

// Review statuses. UNREVIEWED is the only status a generated corpus may hold: a decision requires a signed
// approval that this package never invents.
const (
	StatusUnreviewed = "UNREVIEWED"
	StatusKeep       = "KEEP"
	StatusChange     = "CHANGE"
	StatusHold       = "HOLD"
)

// Speaker applicability. UNDETERMINED is kept apart from NOT_APPLICABLE so a record whose function is unknown
// is never silently declared speaker-free.
const (
	ApplicabilityRequired      = "REQUIRED"
	ApplicabilityNotApplicable = "NOT_APPLICABLE"
	ApplicabilityUndetermined  = "UNDETERMINED"
)

// Label statuses.
const (
	LabelVerified      = "VERIFIED"
	LabelUnknown       = "UNKNOWN"
	LabelNotApplicable = "NOT_APPLICABLE"
	LabelConflict      = "CONFLICT"
)

// MaxGroupRecords and MaxGroupBytes bound one file. A group larger than this is split deterministically.
const (
	MaxGroupRecords = 500
	MaxGroupBytes   = 1 << 20
)

// Group describes what a file holds.
type Group struct {
	Type          string  `json:"type"`
	Namespace     string  `json:"namespace,omitempty"`
	SceneID       *string `json:"scene_id"`
	Chapter       *string `json:"chapter"`
	OrderVerified bool    `json:"order_verified"`
	Part          int     `json:"part,omitempty"`
	Parts         int     `json:"parts,omitempty"`
}

// Source keeps the provenance of a record: which set, namespace and key it came from, the identifiers of both
// original records, and the hashes of the exact texts stored here.
type Source struct {
	Set                string `json:"set"`
	Namespace          string `json:"namespace"`
	Key                string `json:"key"`
	JPRecordID         string `json:"jp_record_id,omitempty"`
	OfficialENRecordID string `json:"official_en_record_id,omitempty"`
	JPSHA256           string `json:"jp_sha256,omitempty"`
	OfficialENSHA256   string `json:"official_en_sha256,omitempty"`
}

// Text holds the three texts. A missing counterpart is null, never a copy of the other language.
type Text struct {
	JP         *string `json:"jp"`
	OfficialEN *string `json:"official_en"`
	RealEN     *string `json:"real_en"`
}

// Review is the editorial state of a record: whether the text is unreviewed, or was reviewed and kept, changed
// or put on hold, with the reason recorded beside it.
type Review struct {
	Status string  `json:"status"`
	Reason *string `json:"reason"`
}

// SpeakerCandidate is one of two or more conflicting labels. Both are kept; neither is chosen.
type SpeakerCandidate struct {
	Label     string   `json:"label"`
	NameEntry string   `json:"name_entry,omitempty"`
	Table     string   `json:"table,omitempty"`
	Evidence  []string `json:"evidence_refs"`
}

// Speaker keeps the four levels apart: the displayed label, the ST_CharaName entry, the character identity and
// the runtime id. A label never becomes an identity.
type Speaker struct {
	Applicability      string             `json:"applicability"`
	LabelJP            *string            `json:"label_jp"`
	LabelStatus        string             `json:"label_status"`
	NameEntry          *string            `json:"name_entry"`
	Identity           *string            `json:"identity"`
	RuntimeCharacterID *string            `json:"runtime_character_id"`
	EvidenceRefs       []string           `json:"evidence_refs"`
	Candidates         []SpeakerCandidate `json:"candidates,omitempty"`
}

// Context is the proved context of a record. Fields nobody proved stay null.
type Context struct {
	SceneRef  *string `json:"scene_ref"`
	Sequence  *int    `json:"sequence"`
	Speaker   Speaker `json:"speaker"`
	Addressee *string `json:"addressee"`
	BranchRef *string `json:"branch_ref"`
	VoiceRef  *string `json:"voice_ref"`
}

// Record is one logical record of the corpus.
type Record struct {
	RecordID string  `json:"record_id"`
	Source   Source  `json:"source"`
	Text     Text    `json:"text"`
	Review   Review  `json:"review"`
	Function string  `json:"function"`
	Context  Context `json:"context"`
}

// File is one self-contained corpus file.
type File struct {
	SchemaVersion int      `json:"schema_version"`
	Group         Group    `json:"group"`
	Records       []Record `json:"records"`
}

// ManifestEntry records one file of the corpus.
type ManifestEntry struct {
	Path      string `json:"path"`
	Type      string `json:"type"`
	Namespace string `json:"namespace,omitempty"`
	Part      int    `json:"part,omitempty"`
	Records   int    `json:"records"`
	SHA256    string `json:"sha256"`
}

// Manifest is translations/manifest.json: what the corpus contains and which format it is.
type Manifest struct {
	SchemaVersion int             `json:"schema_version"`
	Format        string          `json:"format"`
	GeneratedBy   string          `json:"generated_by"`
	Files         []ManifestEntry `json:"files"`
	Counts        ManifestCounts  `json:"counts"`
}

// ManifestCounts are the totals a reader can check without walking every file.
type ManifestCounts struct {
	Files             int            `json:"files"`
	Records           int            `json:"records"`
	JP                int            `json:"jp"`
	OfficialEN        int            `json:"official_en"`
	RealEN            int            `json:"real_en"`
	MissingJP         int            `json:"missing_jp"`
	MissingOfficialEN int            `json:"missing_official_en"`
	ByType            map[string]int `json:"by_type"`
	ByReviewStatus    map[string]int `json:"by_review_status"`
}

// HashText returns the canonical SHA-256 of a text: the exact UTF-8 bytes, no normalisation.
func HashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Dir returns the directory a record belongs in, from its function and namespace.
func Dir(function, namespace string) string {
	switch function {
	case "dialogue_line":
		switch {
		case chapterOf(namespace) != "":
			return filepath.Join("dialogue", "main", chapterOf(namespace))
		case strings.HasPrefix(namespace, "ST_SubEv"), strings.Contains(namespace, "_EV_"):
			return filepath.Join("dialogue", "events")
		case strings.HasPrefix(namespace, "ST_ATC"), strings.HasPrefix(namespace, "ST_Town"),
			strings.HasPrefix(namespace, "ST_FEV"), strings.HasPrefix(namespace, "ST_BtlAI"):
			return filepath.Join("dialogue", "side")
		default:
			return filepath.Join("dialogue", "unassigned")
		}
	case "ui_label", "ui_choice":
		switch {
		case strings.Contains(namespace, "Shop"):
			return filepath.Join("ui", "shop")
		case strings.Contains(namespace, "Btl"), strings.Contains(namespace, "Battle"):
			return filepath.Join("ui", "battle")
		case strings.Contains(namespace, "Menu"):
			return filepath.Join("ui", "menu")
		case strings.Contains(namespace, "System"), strings.Contains(namespace, "Trophy"):
			return filepath.Join("ui", "system")
		default:
			return filepath.Join("ui", "other")
		}
	case "item_name":
		return filepath.Join("items", "names")
	case "item_text":
		return filepath.Join("items", "descriptions")
	case "skill_name":
		return filepath.Join("skills", "names")
	case "skill_text":
		return filepath.Join("skills", "descriptions")
	case "character_name":
		return "characters"
	case "unit_name", "unit_text":
		return "units"
	case "map_name":
		return "maps"
	case "quest_objective", "quest_summary":
		return "quests"
	case "tutorial":
		return "tutorials"
	case "system_message", "trophy":
		return filepath.Join("ui", "system")
	case "engine_or_editor":
		return filepath.Join("other", "engine")
	default:
		return "unclassified"
	}
}

var chapterRe = regexp.MustCompile(`^ST_C(\d{2})_`)

// chapterOf returns the chapter a namespace states in its own name, or "" when it states none. The chapter is
// read from the source namespace, never guessed from a resemblance between names.
func chapterOf(ns string) string {
	m := chapterRe.FindStringSubmatch(ns)
	if m == nil {
		return ""
	}
	return "chapter" + m[1]
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// SafeFileName turns a namespace into a file name that is safe on every filesystem, deterministically.
func SafeFileName(ns string) string {
	if ns == "" {
		return "no-namespace"
	}
	name := unsafeName.ReplaceAllString(ns, "_")
	return strings.Trim(name, "._-")
}

// Load reads every corpus file under dir and returns the files with their parsed contents.
func Load(dir string) ([]File, error) {
	var out []File
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			walkErr := filepath.Walk(filepath.Join(dir, e.Name()), func(p string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if !info.IsDir() && strings.HasSuffix(p, ".json") && !strings.Contains(filepath.ToSlash(p), "/variants/") {
					paths = append(paths, p)
				}
				return nil
			})
			if walkErr != nil {
				return nil, walkErr
			}
			continue
		}
		if strings.HasSuffix(e.Name(), ".json") && e.Name() != "manifest.json" {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var f File
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&f); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if f.SchemaVersion != SchemaVersion {
			return nil, fmt.Errorf("%s: schema version %d, want %d", p, f.SchemaVersion, SchemaVersion)
		}
		out = append(out, f)
	}
	return out, nil
}
