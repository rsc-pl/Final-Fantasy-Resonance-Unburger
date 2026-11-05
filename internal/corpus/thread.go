package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Load reads every JSON record file of one language directory.
func Load(root, lang string) ([]Record, error) {
	dir := filepath.Join(root, "translations", lang)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var rs []Record
		if err := decodeStrict(b, &rs); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, rs...)
	}
	return out, nil
}

// Scenes lists the scene ids present in a language, sorted.
func Scenes(records []Record) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range records {
		if r.Scene != "" && !seen[r.Scene] {
			seen[r.Scene] = true
			out = append(out, r.Scene)
		}
	}
	sort.Strings(out)
	return out
}

// Thread returns one scene in conversation order. Records without a scene are ignored.
func Thread(records []Record, scene string) []Record {
	var out []Record
	for _, r := range records {
		if r.Scene == scene {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Seq != out[j].Seq {
			return out[i].Seq < out[j].Seq
		}
		return out[i].RecordID < out[j].RecordID
	})
	return out
}

// Pair aligns a JP thread with the official translation by record id.
type Pair struct {
	JP  Record
	LOC Record
}

// Pairs returns JP records of a scene joined with their official-EN counterparts.
func Pairs(jp, loc []Record, scene string) []Pair {
	index := make(map[string]Record, len(loc))
	for _, r := range loc {
		index[r.RecordID] = r
	}
	var out []Pair
	for _, r := range Thread(jp, scene) {
		p := Pair{JP: r}
		if l, ok := index[r.RecordID]; ok {
			p.LOC = l
		}
		out = append(out, p)
	}
	return out
}
