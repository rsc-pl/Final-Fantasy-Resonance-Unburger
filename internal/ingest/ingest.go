// Package ingest turns locres inventories into the canonical JP and official-EN corpus.
//
// Identity rules:
//   - record_id is logical and stable: it never contains a content hash, so editing a
//     translation, or shipping an update that changes the text of the same slot, keeps
//     the same id;
//   - the logical set is identified by its key space (hash of the sorted "namespace\0key"
//     pairs), which is language independent and unaffected by text edits;
//   - content_hash and source_version are separate provenance fields.
package ingest

import (
	"crypto/sha1"
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

// Record mirrors corpus.Record but keeps ingest self-contained; the caller writes JSON.
type Record struct {
	RecordID      string `json:"record_id"`
	Resource      string `json:"resource,omitempty"`
	Text          string `json:"text"`
	SourceHash    string `json:"source_hash,omitempty"`
	ContentHash   string `json:"content_hash,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`
	ContextRef    string `json:"context_ref,omitempty"`
	Status        string `json:"status,omitempty"`
	Reason        string `json:"reason,omitempty"`

	// Conversation context. Scene comes from the locres namespace (ST_<scene>); Seq is
	// the numeric tail of the source key. Speaker and Addressee stay UNKNOWN until an
	// asset-level proof exists, and carry their own confidence so nothing is implied.
	Scene             string `json:"scene,omitempty"`
	Seq               int    `json:"seq,omitempty"`
	PrevRecordID      string `json:"prev_record_id,omitempty"`
	NextRecordID      string `json:"next_record_id,omitempty"`
	Speaker           string `json:"speaker,omitempty"`
	Addressee         string `json:"addressee,omitempty"`
	SpeakerConfidence string `json:"speaker_confidence,omitempty"`
	SpeakerEvidence   string `json:"speaker_evidence,omitempty"`

	// Branch evidence: the choice token from the scene id, corroborated by the sequencer
	// assets the build ships under Sequencer/Event/*/Choice<N>/.
	Branch           string `json:"branch,omitempty"`
	BranchEvidence   string `json:"branch_evidence,omitempty"`
	BranchConfidence string `json:"branch_confidence,omitempty"`

	// Scene-to-asset evidence from the build manifest (cooked file list).
	SceneAssetDir   string `json:"scene_asset_dir,omitempty"`
	SceneEvidence   string `json:"scene_evidence,omitempty"`
	SceneConfidence string `json:"scene_confidence,omitempty"`
}

type inventory struct {
	Platform      string `json:"platform"`
	Source        string `json:"source"`
	SHA256        string `json:"sha256"`
	Version       uint8  `json:"version"`
	SourceVersion string `json:"source_version,omitempty"`
	Entries       []struct {
		Namespace  string `json:"namespace"`
		Key        string `json:"key"`
		Text       string `json:"text"`
		SourceHash uint32 `json:"source_hash"`
	} `json:"entries"`
}

// Report summarises one ingest run.
type Report struct {
	Sets             []SetReport
	JP               int
	LOC              int
	Excluded         map[string]int
	ExclPath         string
	Written          []string
	SkippedNo        int
	Candidates       []string
	Diffs            []SetDiff
	Branches         int
	ScenesWithAssets int
}

// SetReport describes one logical localisation set.
type SetReport struct {
	Signature string
	Records   int
	JASource  string
	ENSource  string
	Class     string
	JALayer   string
	ENLayer   string
}

func (r Report) String() string {
	var b strings.Builder
	for _, s := range r.Sets {
		fmt.Fprintf(&b, "%-8s records=%-6d class=%-9s layer=%-14s ja=%s en=%s\n", s.Signature, s.Records, s.Class, s.JALayer, s.JASource, s.ENSource)
	}
	fmt.Fprintf(&b, "corpus: jp=%d loc=%d excluded=%v\n", r.JP, r.LOC, r.Excluded)
	for _, w := range r.Written {
		fmt.Fprintf(&b, "wrote %s\n", w)
	}
	if r.SkippedNo > 0 {
		fmt.Fprintf(&b, "sets without a JP/EN pair: %d\n", r.SkippedNo)
	}
	for _, d := range r.Diffs {
		fmt.Fprintf(&b, "layer %s -> %s on %s: added=%d removed=%d changed=%d unchanged=%d\n",
			d.From, d.To, d.Set, d.Added, d.Removed, d.Changed, d.Unchanged)
	}
	if r.ScenesWithAssets > 0 {
		fmt.Fprintf(&b, "scenes linked to shipped sequencer assets: %d (records annotated: %d)\n", r.ScenesWithAssets, r.Branches)
	}
	for _, c := range r.Candidates {
		fmt.Fprintf(&b, "identity candidate (Jaccard only, NOT accepted): %s\n", c)
	}
	return b.String()
}

var slugRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(s, "-"), "-")
	if s == "" {
		return "x"
	}
	if len(s) > 96 {
		s = s[:96]
	}
	return s
}

// overlap is the Jaccard similarity of two key sets.
func overlap(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	small, big := a, b
	if len(b) < len(a) {
		small, big = b, a
	}
	for k := range small {
		if big[k] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	return float64(inter) / float64(union)
}

// signature hashes the key space of a set: stable across text edits and language.
func signature(inv inventory) string {
	keys := make([]string, 0, len(inv.Entries))
	for _, e := range inv.Entries {
		keys = append(keys, e.Namespace+"\x00"+e.Key)
	}
	sort.Strings(keys)
	h := sha1.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

var stopwords = map[string][]string{
	"en": {" the ", " and ", " you ", " is ", " to ", " of "},
	"de": {" der ", " und ", " nicht ", " ist ", " das ", " ich "},
	"fr": {" le ", " les ", " est ", " vous ", " que ", " pas "},
	"es": {" el ", " los ", " que ", " es ", " no ", " por "},
	"is": {" og ", " ekki ", " er ", " að ", " þú ", " með "},
}

// language classifies an inventory by character-level script weight, then by stop-word
// density. Presence alone is not enough: shipped English archives still carry a few
// untranslated Japanese lines, so a single kana character must not decide the language.
func language(inv inventory) string {
	var kana, hangul, han, latin int
	for _, e := range inv.Entries {
		for _, r := range e.Text {
			switch {
			case r >= '぀' && r <= 'ヿ':
				kana++
			case r >= '가' && r <= '힯':
				hangul++
			case r >= '一' && r <= '鿿':
				han++
			case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
				latin++
			}
		}
	}
	total := kana + hangul + han + latin
	if total == 0 {
		return "unknown"
	}
	share := func(n int) float64 { return float64(n) / float64(total) }
	switch {
	case share(kana) >= 0.30:
		return "ja"
	case share(hangul) >= 0.30:
		return "ko"
	case share(han) >= 0.30:
		return "zh"
	}

	var sample strings.Builder
	for _, e := range inv.Entries {
		sample.WriteString(strings.ToLower(e.Text))
		sample.WriteString(" ")
		if sample.Len() > 2_000_000 {
			break
		}
	}
	text := sample.String()
	best, bestScore := "", 0
	for lang, words := range stopwords {
		score := 0
		for _, w := range words {
			score += strings.Count(text, w)
		}
		if score > bestScore {
			best, bestScore = lang, score
		}
	}
	if best == "" {
		return "unknown"
	}
	return best
}

// Run reads every inventory and writes the extracted source texts to the private staging area. Those files are
// the input of the canonical corpus build, not a canonical corpus of their own.
func Run(root string) (Report, error) {
	rep := Report{Excluded: map[string]int{}}
	dir := filepath.Join(root, ".local", "extract", "inventory")
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return rep, err
	}
	if len(files) == 0 {
		return rep, fmt.Errorf("no inventories under %s; run `extract` first", dir)
	}
	sort.Strings(files)

	type loaded struct {
		inv  inventory
		lang string
		keys map[string]bool
	}
	var all []loaded
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return rep, err
		}
		var inv inventory
		if err := json.Unmarshal(b, &inv); err != nil {
			return rep, fmt.Errorf("%s: %w", f, err)
		}
		if len(inv.Entries) == 0 {
			continue
		}
		keys := make(map[string]bool, len(inv.Entries))
		for _, e := range inv.Entries {
			keys[e.Namespace+"\x00"+e.Key] = true
		}
		all = append(all, loaded{inv: inv, lang: language(inv), keys: keys})
	}

	// Cluster archives into logical sets by key-space overlap. Language archives of the
	// same set differ slightly (a few untranslated slots), so exact signatures cannot
	// group them; overlap can. The set id itself is taken from the Japanese member only.
	var clusters [][]loaded
	used := make([]bool, len(all))
	for i := range all {
		if used[i] {
			continue
		}
		used[i] = true
		cluster := []loaded{all[i]}
		for j := i + 1; j < len(all); j++ {
			if used[j] {
				continue
			}
			if overlap(all[i].keys, all[j].keys) >= 0.90 {
				used[j] = true
				cluster = append(cluster, all[j])
			}
		}
		clusters = append(clusters, cluster)
	}

	var jp, loc []Record
	var excluded []Record
	reg, err := loadRegistry(root)
	if err != nil {
		return rep, err
	}
	layerRank := func(layer string) int {
		if strings.HasSuffix(layer, "-update") {
			return 2
		}
		if layer == "" {
			return 0
		}
		return 1
	}
	var diffs []SetDiff
	for _, group := range clusters {
		var jaNew, enNew, jaOld, enOld *loaded
		for i := range group {
			switch group[i].lang {
			case "ja":
				if jaNew == nil || layerRank(group[i].inv.SourceVersion) > layerRank(jaNew.inv.SourceVersion) {
					jaOld, jaNew = jaNew, &group[i]
				} else if jaOld == nil || layerRank(group[i].inv.SourceVersion) > layerRank(jaOld.inv.SourceVersion) {
					jaOld = &group[i]
				}
			case "en":
				if enNew == nil || layerRank(group[i].inv.SourceVersion) > layerRank(enNew.inv.SourceVersion) {
					enOld, enNew = enNew, &group[i]
				} else if enOld == nil || layerRank(group[i].inv.SourceVersion) > layerRank(enOld.inv.SourceVersion) {
					enOld = &group[i]
				}
			}
		}
		if jaNew == nil || enNew == nil {
			rep.SkippedNo++
			rep.Sets = append(rep.Sets, SetReport{Records: len(group[0].inv.Entries), Class: "NO_PAIR"})
			continue
		}

		setID, candidate, proven := reg.match(jaNew.inv.Platform, jaNew.keys, nil)
		if !proven {
			setID = reg.mint(jaNew.inv.Platform, sortedKeys(jaNew.keys), jaNew.inv.SourceVersion)
			if candidate != "" {
				// Overlap alone is never accepted as identity: the candidate is recorded and
				// the archive gets its own id until anchors prove the link.
				rep.Candidates = append(rep.Candidates, setID+" ~ "+candidate)
			}
		}

		if jaOld != nil && layerRank(jaNew.inv.SourceVersion) > layerRank(jaOld.inv.SourceVersion) {
			diffs = append(diffs, diffLayers(setID, jaNew.inv.Platform,
				indexOf(jaOld.inv), indexOf(jaNew.inv), jaOld.inv.SourceVersion, jaNew.inv.SourceVersion))
		}

		enIndex := make(map[string]int, len(enNew.inv.Entries))
		for i, e := range enNew.inv.Entries {
			enIndex[e.Namespace+"\x00"+e.Key] = i
		}
		kept := 0
		for _, je := range jaNew.inv.Entries {
			key := je.Namespace + "\x00" + je.Key
			idx, ok := enIndex[key]
			if !ok {
				excluded = append(excluded, Record{RecordID: "unpaired:" + setID + ":" + slug(je.Key), Text: je.Text, Reason: "NO_EN_COUNTERPART"})
				rep.Excluded["NO_EN_COUNTERPART"]++
				continue
			}
			enText := enNew.inv.Entries[idx].Text
			jpOK, enOK := strings.TrimSpace(je.Text) != "", strings.TrimSpace(enText) != ""
			if !jpOK && !enOK {
				excluded = append(excluded, Record{RecordID: "empty:" + setID + ":" + slug(je.Key), Text: "-", Reason: "BOTH_EMPTY"})
				rep.Excluded["BOTH_EMPTY"]++
				continue
			}
			if !jpOK {
				excluded = append(excluded, Record{RecordID: "en-only:" + setID + ":" + slug(je.Key), Text: enText, Reason: "EN_ONLY"})
				rep.Excluded["EN_ONLY"]++
				continue
			}
			id := recordID(jaNew.inv.Platform, setID, je.Namespace, je.Key)
			ctx := "locres namespace=" + je.Namespace + " key=" + je.Key
			jp = append(jp, Record{
				RecordID: id, Resource: jaNew.inv.Source, Text: je.Text,
				SourceHash: jaNew.inv.SHA256, ContentHash: textHash(je.Text),
				SourceVersion: jaNew.inv.SourceVersion, ContextRef: ctx,
			})
			if enOK {
				loc = append(loc, Record{
					RecordID: id, Resource: enNew.inv.Source, Text: enText,
					SourceHash: enNew.inv.SHA256, ContentHash: textHash(enText),
					SourceVersion: enNew.inv.SourceVersion, ContextRef: ctx,
				})
			} else {
				excluded = append(excluded, Record{RecordID: id, Text: je.Text, Reason: "JP_ONLY"})
				rep.Excluded["JP_ONLY"]++
			}
			kept++
		}
		rep.Sets = append(rep.Sets, SetReport{Signature: setID, Records: kept, Class: "ALIGNED",
			JASource: jaNew.inv.SHA256[:16], ENSource: enNew.inv.SHA256[:16],
			JALayer: jaNew.inv.SourceVersion, ENLayer: enNew.inv.SourceVersion})
	}
	if err := reg.save(); err != nil {
		return rep, err
	}
	rep.Diffs = diffs
	AnnotateDialogue(jp, loc)
	manifest := filepath.Join(root, ".local", "originals", "switch", "romfs", "Manifest_UFSFiles_Switch.txt")
	rep.Branches, rep.ScenesWithAssets = AnnotateSceneAssets(jp, loc, LoadAssetIndex(manifest))
	rep.JP, rep.LOC = len(jp), len(loc)
	for _, w := range []struct {
		path    string
		records []Record
	}{{".local/staging/source/jp.json", jp}, {".local/staging/source/loc.json", loc}} {
		if err := writeRecords(filepath.Join(root, w.path), w.records); err != nil {
			return rep, err
		}
		rep.Written = append(rep.Written, w.path)
	}
	if err := writeSceneRegistry(root, jp); err != nil {
		return rep, err
	}
	if len(rep.Diffs) > 0 {
		p := filepath.Join(root, ".local", "work", "import", "layer-diff.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return rep, err
		}
		b, err := json.MarshalIndent(rep.Diffs, "", " ")
		if err != nil {
			return rep, err
		}
		b = append(b, '\n')
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return rep, err
		}
	}

	if len(excluded) > 0 {
		p := filepath.Join(root, ".local", "work", "import", "exclusions.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return rep, err
		}
		if err := writeRecords(p, excluded); err != nil {
			return rep, err
		}
		rep.ExclPath = p
	}
	return rep, nil
}

// recordID builds the logical identity of one slot. It depends only on the platform, the
// set id, the namespace and the key: never on the text, the archive hash or the layer, so a
// translation edit or an update that rewrites the text keeps the same id.
func recordID(platform, setID, namespace, key string) string {
	d := sha1.Sum([]byte(namespace + "\x00" + key))
	return fmt.Sprintf("%s:locres:%s:%s:%s:%s", platform, setID, slug(namespace), slug(key), hex.EncodeToString(d[:])[:8])
}

// sceneEntry is one row of the private scene registry.
type sceneEntry struct {
	Scene     string `json:"scene"`
	Lines     int    `json:"lines"`
	FirstSeq  int    `json:"first_seq"`
	LastSeq   int    `json:"last_seq"`
	Speaker   string `json:"speaker"`
	Addressee string `json:"addressee"`
	Evidence  string `json:"evidence"`
}

// writeSceneRegistry records every dialogue scene found in the corpus. Speaker data stays
// UNKNOWN: a locres archive proves the text and the order, not who speaks.
func writeSceneRegistry(root string, jp []Record) error {
	byScene := map[string][]Record{}
	for _, r := range jp {
		if r.Scene != "" {
			byScene[r.Scene] = append(byScene[r.Scene], r)
		}
	}
	scenes := make([]string, 0, len(byScene))
	for s := range byScene {
		scenes = append(scenes, s)
	}
	sort.Strings(scenes)
	out := make([]sceneEntry, 0, len(scenes))
	for _, s := range scenes {
		rows := byScene[s]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Seq < rows[j].Seq })
		out = append(out, sceneEntry{
			Scene: s, Lines: len(rows),
			FirstSeq: rows[0].Seq, LastSeq: rows[len(rows)-1].Seq,
			Speaker: "UNKNOWN", Addressee: "UNKNOWN",
			Evidence: "locres archive has no speaker, addressee or branch field",
		})
	}
	path := filepath.Join(root, ".local", "toolchain", "records", "scenes.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	stage, err := os.CreateTemp(filepath.Dir(path), "ffrs-tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(stage.Name())
	if _, err := stage.Write(b); err != nil {
		stage.Close()
		return err
	}
	if err := stage.Close(); err != nil {
		return err
	}
	return os.Rename(stage.Name(), path)
}

func textHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func writeRecords(path string, records []Record) error {
	if records == nil {
		records = []Record{}
	}
	out, err := json.MarshalIndent(records, "", " ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	stage, err := os.CreateTemp(filepath.Dir(path), "ffrs-tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(stage.Name())
	if _, err := stage.Write(out); err != nil {
		stage.Close()
		return err
	}
	if err := stage.Close(); err != nil {
		return err
	}
	return os.Rename(stage.Name(), path)
}
