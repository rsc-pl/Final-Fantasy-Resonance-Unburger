package corpuspack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"unburger/internal/zen"
)

// srcRecord is one record of a legacy mono-language file, read only by the migration tool.
type srcRecord struct {
	RecordID   string `json:"record_id"`
	Resource   string `json:"resource"`
	Text       string `json:"text"`
	ContextRef string `json:"context_ref"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
}

// BuildOptions names the inputs of a corpus build.
type BuildOptions struct {
	JP     string
	Loc    string
	RealTL string
	Keys   string
	OutDir string
	// Existing is a corpus tree whose real translations and reviews must survive the rebuild. Without it a
	// rebuild would silently drop work imported from the translation pass.
	Existing string
}

// BuildResult reports what was written.
type BuildResult struct {
	Manifest Manifest
	Records  int
}

// splitSource turns a context reference into namespace and key.
func splitSource(ctx string) (string, string) {
	ns, k := "", ""
	for _, f := range strings.Fields(ctx) {
		if strings.HasPrefix(f, "namespace=") {
			ns = strings.TrimPrefix(f, "namespace=")
		}
		if strings.HasPrefix(f, "key=") {
			k = strings.TrimPrefix(f, "key=")
		}
	}
	return ns, k
}

// identityKey is the only join the corpus allows: the source set, the namespace and the key. A bare key is
// never enough, because the same key exists in more than one namespace.
func identityKey(ns, key string) string { return ns + "\x00" + key }

// group is the in-memory accumulation of one output file.
type group struct {
	dir  string
	typ  string
	ns   string
	recs []Record
	size int
}

// Build assembles the split corpus from the three mono files and the proved key identities, writes it under
// OutDir and returns the manifest. It never assigns an editorial status other than UNREVIEWED.
func Build(opt BuildOptions) (BuildResult, error) {
	jpRaw, err := readRecords(opt.JP)
	if err != nil {
		return BuildResult{}, err
	}
	locRaw, err := readRecords(opt.Loc)
	if err != nil {
		return BuildResult{}, err
	}
	realRaw, err := readRecords(opt.RealTL)
	if err != nil {
		if !os.IsNotExist(err) && !strings.Contains(err.Error(), "no such file") {
			return BuildResult{}, err
		}
		realRaw = nil
	}
	bindings, err := loadBindings(opt.Keys)
	if err != nil {
		return BuildResult{}, err
	}
	// The dialogue namespaces come from the same key-identities file the speaker data comes from, so a build
	// never depends on a caller remembering to install them.
	nsSet := map[string]bool{}
	for _, b := range bindings {
		if b.Namespace != "" {
			nsSet[b.Namespace] = true
		}
	}
	SetDialogueNamespaces(nsSet)
	// A key that occurs more than once in the Japanese corpus does not identify one record, so it is never
	// joined to an official English record: joining would attach the wrong translation to one of the copies.
	jpCount := map[string]int{}
	for _, j := range jpRaw {
		ns, k := splitSource(j.ContextRef)
		jpCount[identityKey(ns, k)]++
	}
	// Where a key repeats inside one namespace the copies are paired by their order in both corpora: the first
	// Japanese copy with the first English copy, and so on. That is deterministic and loses nothing, and it is
	// recorded here rather than hidden, because the pairing of duplicates is an assumption and not a proof.
	locQueue := map[string][]srcRecord{}
	for _, l := range locRaw {
		ns, k := splitSource(l.ContextRef)
		locQueue[identityKey(ns, k)] = append(locQueue[identityKey(ns, k)], l)
	}
	realByID := map[string]srcRecord{}
	for _, r := range realRaw {
		realByID[r.RecordID] = r
	}
	// Carry the current real translations and their reviews across a rebuild, by record id.
	carried := map[string]Record{}
	if opt.Existing != "" {
		if prev, err := LoadIndex(opt.Existing); err == nil {
			for id, r := range prev.ByID {
				if r.Text.RealEN != nil || r.Review.Status != StatusUnreviewed {
					carried[id] = r
					// A record is also reachable by the identifier of its original source row, so a rebuild that
					// assigns ids from the sources still finds the translation that was made against the old id.
					if r.Source.JPRecordID != "" {
						carried[r.Source.JPRecordID] = r
					}
				}
			}
		}
	}
	applyCarried := func(rec *Record) {
		if prev, ok := carried[rec.RecordID]; ok {
			rec.Text.RealEN = prev.Text.RealEN
			rec.Review = prev.Review
		}
	}

	groups := map[string]*group{}
	order := []string{}
	add := func(rec Record, dir, typ, ns string) {
		key := dir + "\x00" + typ + "\x00" + ns
		g := groups[key]
		if g == nil {
			g = &group{dir: dir, typ: typ, ns: ns}
			groups[key] = g
			order = append(order, key)
		}
		g.recs = append(g.recs, rec)
		g.size += len(rec.Text.JPOrEmpty()) + len(rec.Text.OfficialENOrEmpty()) + 256
	}

	// Japanese records first, in source order: the order the corpus stores is the order the source had.
	seen := map[string]bool{}
	for _, j := range jpRaw {
		ns, k := splitSource(j.ContextRef)
		id := identityKey(ns, k)
		seen[id] = true
		var locRec *srcRecord
		if q := locQueue[id]; len(q) > 0 {
			l := q[0]
			locQueue[id] = q[1:]
			locRec = &l
		}
		rec := newRecord(j, locRec, ns, k, bindings[id])
		applyCarried(&rec)
		add(rec, Dir(rec.Function, ns), rec.Function, ns)
	}
	// Records that exist only in the official English corpus: kept, with a null Japanese side.
	for _, l := range locRaw {
		ns, k := splitSource(l.ContextRef)
		id := identityKey(ns, k)
		if seen[id] {
			continue
		}
		seen[id] = true
		rec := newRecord(srcRecord{}, &l, ns, k, bindings[id])
		applyCarried(&rec)
		add(rec, Dir(rec.Function, ns), rec.Function, ns)
	}
	// A real translation may exist for a record the other two corpora lack; it is a record too.
	for _, r := range realRaw {
		ns, k := splitSource(r.ContextRef)
		id := identityKey(ns, k)
		if seen[id] {
			continue
		}
		seen[id] = true
		rec := newRecord(srcRecord{}, nil, ns, k, bindings[id])
		applyCarried(&rec)
		add(rec, Dir(rec.Function, ns), rec.Function, ns)
	}

	if err := os.MkdirAll(opt.OutDir, 0o755); err != nil {
		return BuildResult{}, err
	}
	keys := make([]string, 0, len(order))
	keys = append(keys, order...)
	sort.Strings(keys)
	man := Manifest{
		SchemaVersion: SchemaVersion,
		Format:        FormatName,
		GeneratedBy:   "unburger corpus build",
		Counts: ManifestCounts{
			ByType:         map[string]int{},
			ByReviewStatus: map[string]int{},
		},
	}
	for _, key := range keys {
		g := groups[key]
		parts := splitGroup(g)
		total := len(parts)
		for i, part := range parts {
			grp := Group{
				Type:          g.typ,
				Namespace:     g.ns,
				OrderVerified: false,
			}
			if g.typ == "dialogue_line" {
				s := g.ns
				grp.SceneID = &s
			}
			if ch := chapterOf(g.ns); ch != "" {
				grp.Chapter = &ch
			}
			if total > 1 {
				grp.Part = i + 1
				grp.Parts = total
			}
			f := File{SchemaVersion: SchemaVersion, Group: grp, Records: part}
			name := SafeFileName(g.ns)
			if name == "" {
				name = "no-namespace"
			}
			if total > 1 {
				name = fmt.Sprintf("%s.part%02d", name, i+1)
			}
			path := filepath.Join(opt.OutDir, g.dir, name+".json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return BuildResult{}, err
			}
			blob, err := marshalIndent(f)
			if err != nil {
				return BuildResult{}, err
			}
			if err := os.WriteFile(path, blob, 0o644); err != nil {
				return BuildResult{}, err
			}
			rel, err := filepath.Rel(opt.OutDir, path)
			if err != nil {
				rel = path
			}
			man.Files = append(man.Files, ManifestEntry{
				Path:      filepath.ToSlash(rel),
				Type:      g.typ,
				Namespace: g.ns,
				Part:      grp.Part,
				Records:   len(part),
				SHA256:    HashText(string(blob)),
			})
			man.Counts.Files++
			man.Counts.Records += len(part)
			man.Counts.ByType[g.typ] += len(part)
			for _, r := range part {
				if r.Text.JP != nil {
					man.Counts.JP++
				} else {
					man.Counts.MissingJP++
				}
				if r.Text.OfficialEN != nil {
					man.Counts.OfficialEN++
				} else {
					man.Counts.MissingOfficialEN++
				}
				if r.Text.RealEN != nil {
					man.Counts.RealEN++
				}
				man.Counts.ByReviewStatus[r.Review.Status]++
			}
		}
	}
	blob, err := marshalIndent(man)
	if err != nil {
		return BuildResult{}, err
	}
	if err := os.WriteFile(filepath.Join(opt.OutDir, "manifest.json"), blob, 0o644); err != nil {
		return BuildResult{}, err
	}
	return BuildResult{Manifest: man, Records: man.Counts.Records}, nil
}

// newRecord assembles one corpus record. The editorial status is always UNREVIEWED: a build is not a review.
func newRecord(j srcRecord, l *srcRecord, ns, key string, b *zen.KeyBinding) Record {
	rec := Record{
		Source: Source{Set: "set-02", Namespace: ns, Key: key},
		Review: Review{Status: StatusUnreviewed},
	}
	if j.RecordID != "" {
		rec.Source.JPRecordID = j.RecordID
		rec.RecordID = j.RecordID
		txt := j.Text
		rec.Text.JP = &txt
		rec.Source.JPSHA256 = HashText(txt)
	}
	if l != nil && l.RecordID != "" {
		rec.Source.OfficialENRecordID = l.RecordID
		if rec.RecordID == "" {
			rec.RecordID = l.RecordID
		}
		txt := l.Text
		rec.Text.OfficialEN = &txt
		rec.Source.OfficialENSHA256 = HashText(txt)
	}
	if rec.RecordID == "" {
		rec.RecordID = "local:" + ns + ":" + key
	}
	rec.Function, _ = functionOf(ns, b)
	rec.Context.Speaker = speakerFor(rec.Function, b)
	if rec.Function == "dialogue_line" && ns != "" {
		s := ns
		rec.Context.SceneRef = &s
	}
	rec.Context.Speaker.EvidenceRefs = []string{}
	if b != nil && b.Namespace != "" {
		rec.Context.Speaker.EvidenceRefs = append(rec.Context.Speaker.EvidenceRefs, "key-identities:"+b.Namespace+"/"+b.Key)
	}
	return rec
}

// functionOf returns the source-justified function of a record and whether a speaker applies to it.
func functionOf(ns string, b *zen.KeyBinding) (string, string) {
	if dialogueNamespaces()[ns] {
		return "dialogue_line", "APPLICABLE"
	}
	fn, sp := zen.FunctionFor(ns, dialogueNamespaces())
	if fn != zen.FunctionUnclassified {
		return fn, sp
	}
	// A namespace outside ST_ holds Unreal editor or plugin interface text: no game table references it and
	// its records read as editor strings. A namespace inside ST_ that no family claims stays unclassified.
	if !strings.HasPrefix(ns, "ST_") {
		return "engine_or_editor", "NOT_APPLICABLE"
	}
	return "game_text_unclassified", "UNDETERMINED"
}

func (t Text) JPOrEmpty() string         { return deref(t.JP) }
func (t Text) OfficialENOrEmpty() string { return deref(t.OfficialEN) }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// splitGroup cuts a group into parts of at most MaxGroupRecords records or MaxGroupBytes bytes, in source order.
func splitGroup(g *group) [][]Record {
	if len(g.recs) <= MaxGroupRecords && g.size <= MaxGroupBytes {
		return [][]Record{g.recs}
	}
	var parts [][]Record
	var cur []Record
	size := 0
	for _, r := range g.recs {
		rs := len(r.Text.JPOrEmpty()) + len(r.Text.OfficialENOrEmpty()) + 256
		if len(cur) > 0 && (len(cur) >= MaxGroupRecords || size+rs > MaxGroupBytes) {
			parts = append(parts, cur)
			cur = nil
			size = 0
		}
		cur = append(cur, r)
		size += rs
	}
	if len(cur) > 0 {
		parts = append(parts, cur)
	}
	return parts
}

func readRecords(path string) ([]srcRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []srcRecord
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

func loadBindings(path string) (map[string]*zen.KeyBinding, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rep struct {
		Bindings []zen.KeyBinding `json:"bindings"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// An empty binding table is not an error here: a corpus can be built from sources alone, in which case no
	// speaker has been proved yet and every dialogue record keeps an unknown label.
	bs := rep.Bindings
	out := make(map[string]*zen.KeyBinding, len(bs))
	for i := range bs {
		b := bs[i]
		out[identityKey(b.Namespace, b.Key)] = &b
	}
	return out, nil
}

// dialogueNamespaces is the set of namespaces a dialogue table was placed in; it decides whether a speaker can
// apply to a record at all.
func dialogueNamespaces() map[string]bool {
	if cache != nil {
		return cache
	}
	return map[string]bool{}
}

var cache map[string]bool

// SetDialogueNamespaces installs the assigned dialogue namespaces before a build.
func SetDialogueNamespaces(ns map[string]bool) { cache = ns }

func marshalIndent(v interface{}) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
