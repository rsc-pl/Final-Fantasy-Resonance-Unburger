package ingest

import (
	"os"
	"regexp"
	"sort"
	"strings"
)

// Scene ↔ asset evidence.
//
// The build manifest is a real artifact of the shipped game: it lists every cooked file. It
// proves that a scene id has sequencer assets behind it, and whether those assets live under
// a Choice<N> directory. It does not prove who speaks a line — only an asset-level parse (or
// runtime capture) can do that — so speaker fields stay untouched here.
type sceneAsset struct {
	Dir      string // FFRS/Content/Sequencer/Event/<Event>
	Files    int
	Branches map[string]bool
}

// AssetIndex maps lower-cased event directory names to what the build ships for them.
type AssetIndex map[string]*sceneAsset

var eventDir = regexp.MustCompile(`^(FFRS/Content/Sequencer/Event/[^/]+/[^/]+)/`)

// choiceToken matches a Choice<N> path segment or scene suffix.
var choiceToken = regexp.MustCompile(`(?i)^(?:.*_)?(choice[0-9]+)$`)

// LoadAssetIndex reads a manifest and collects sequencer event directories.
func LoadAssetIndex(manifestPath string) AssetIndex {
	idx := AssetIndex{}
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return idx
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		p := strings.SplitN(line, "\t", 2)[0]
		m := eventDir.FindStringSubmatch(p)
		if m == nil {
			continue
		}
		key := strings.ToLower(m[1])
		e := idx[key]
		if e == nil {
			e = &sceneAsset{Dir: m[1], Branches: map[string]bool{}}
			idx[key] = e
		}
		e.Files++
		for _, seg := range strings.Split(p, "/") {
			if choiceToken.MatchString(seg) {
				e.Branches[strings.ToLower(seg)] = true
			}
		}
	}
	return idx
}

// resolveAsset maps a locres scene id onto one shipped event directory. It requires exactly
// one directory whose name contains the scene token; several matches are reported as
// ambiguous instead of picking one.
func (idx AssetIndex) resolveAsset(scene string) (*sceneAsset, bool) {
	token := strings.ToLower(strings.TrimPrefix(scene, "ST_"))
	if token == "" {
		return nil, false
	}
	keys := make([]string, 0, len(idx))
	for k := range idx {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Tier 1: the directory name itself is the scene token. Strongest evidence.
	var exact []string
	for _, k := range keys {
		if strings.HasSuffix(k, "/"+token) {
			exact = append(exact, k)
		}
	}
	if len(exact) == 1 {
		return idx[exact[0]], true
	}
	if len(exact) > 1 {
		return nil, false
	}
	// Tier 2: a single directory whose path contains the token.
	var hits []string
	for _, k := range keys {
		if strings.Contains(k, token) {
			hits = append(hits, k)
		}
	}
	if len(hits) != 1 {
		return nil, false
	}
	return idx[hits[0]], true
}

// AnnotateSceneAssets records which shipped sequencer directory backs each scene, and the
// branches that directory contains. Returns the number of records annotated.
func AnnotateSceneAssets(jp, loc []Record, idx AssetIndex) (annotated int, resolved int) {
	type info struct {
		dir, branch, evidence, confidence string
	}
	cache := map[string]info{}
	for _, r := range jp {
		if r.Scene == "" {
			continue
		}
		if _, ok := cache[r.Scene]; !ok {
			if e, ok := idx.resolveAsset(r.Scene); ok {
				in := info{dir: e.Dir, evidence: "sequencer assets: " + e.Dir + " (" + itoa(e.Files) + " files)", confidence: "asset-path"}
				if len(e.Branches) == 1 {
					for b := range e.Branches {
						in.branch = strings.Title(b) //nolint:staticcheck // ASCII tokens only
					}
				} else if len(e.Branches) > 1 {
					in.evidence += "; several Choice directories, branch not assigned"
				}
				cache[r.Scene] = in
			} else {
				cache[r.Scene] = info{}
			}
		}
	}
	apply := func(rs []Record) int {
		n := 0
		for i := range rs {
			in, ok := cache[rs[i].Scene]
			if !ok || in.dir == "" {
				continue
			}
			rs[i].SceneAssetDir = in.dir
			rs[i].SceneEvidence = in.evidence
			rs[i].SceneConfidence = in.confidence
			if in.branch != "" {
				rs[i].Branch = in.branch
				rs[i].BranchEvidence = "single Choice directory under " + in.dir
				rs[i].BranchConfidence = "asset-path"
			}
			n++
		}
		return n
	}
	annotated = apply(jp)
	apply(loc)
	for _, in := range cache {
		if in.dir != "" {
			resolved++
		}
	}
	return annotated, resolved
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
