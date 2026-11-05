package ingest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Set registry.
//
// A set id must survive updates that add or delete records. Hashing the key space at every
// run would churn the id the first time a key appears, so ids are minted once and stored.
// Matching a newly read archive to a stored set needs positive evidence, not similarity
// alone: the archive must contain every anchor key recorded for that set *and* overlap the
// stored key space. Jaccard overlap by itself only proposes a candidate, it never decides.
type setRecord struct {
	ID       string   `json:"id"`
	Platform string   `json:"platform"`
	Anchors  []string `json:"anchors"`
	Created  string   `json:"created_in"`
	Records  int      `json:"records_at_creation"`
	Note     string   `json:"note,omitempty"`
}

type registry struct {
	path string
	Sets []setRecord `json:"sets"`
}

func loadRegistry(root string) (*registry, error) {
	r := &registry{path: filepath.Join(root, ".local", "toolchain", "records", "sets.json")}
	b, err := os.ReadFile(r.path)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, fmt.Errorf("sets registry: %w", err)
	}
	return r, nil
}

func (r *registry) save() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", " ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	stage, err := os.CreateTemp(filepath.Dir(r.path), "ffrs-tmp-")
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
	return os.Rename(stage.Name(), r.path)
}

// pickAnchors spreads anchors across the sorted key space so that deleting a few keys does
// not invalidate the identity evidence.
func pickAnchors(keys []string) []string {
	n := len(keys)
	if n == 0 {
		return nil
	}
	want := 11
	if n < want {
		want = n
	}
	out := make([]string, 0, want)
	seen := map[string]bool{}
	for i := 0; i < want; i++ {
		idx := 0
		if want > 1 {
			idx = i * (n - 1) / (want - 1)
		}
		if !seen[keys[idx]] {
			seen[keys[idx]] = true
			out = append(out, keys[idx])
		}
	}
	return out
}

// match returns the stored set that the given keys prove membership of, if any.
// A high overlap alone is reported as a candidate and deliberately not accepted.
func (r *registry) match(platform string, keys map[string]bool, anchors []string) (id string, candidate string, overlapOK bool) {
	bestID, bestOverlap := "", 0.0
	for _, s := range r.Sets {
		if s.Platform != platform {
			continue
		}
		all := true
		for _, a := range s.Anchors {
			if !keys[a] {
				all = false
				break
			}
		}
		o := overlapWithAnchors(keys, s.Anchors, s.Records)
		if o > bestOverlap {
			bestOverlap, bestID = o, s.ID
		}
		if all && o >= 0.90 {
			return s.ID, "", true
		}
	}
	if bestOverlap >= 0.90 {
		return "", bestID, false
	}
	_ = anchors
	return "", "", false
}

// overlapWithAnchors estimates overlap when only the anchor list and the original size are
// known: the archive must contain the anchors, and its size must be within 10% of the
// recorded size.
func overlapWithAnchors(keys map[string]bool, anchors []string, recorded int) float64 {
	if recorded == 0 || len(anchors) == 0 {
		return 0
	}
	hit := 0
	for _, a := range anchors {
		if keys[a] {
			hit++
		}
	}
	ratio := float64(hit) / float64(len(anchors))
	size := float64(len(keys)) / float64(recorded)
	if size > 1 {
		size = 1 / size
	}
	return ratio * size
}

func (r *registry) mint(platform string, keys []string, layer string) string {
	id := fmt.Sprintf("set-%02d", len(r.Sets)+1)
	r.Sets = append(r.Sets, setRecord{
		ID:       id,
		Platform: platform,
		Anchors:  pickAnchors(keys),
		Created:  layer,
		Records:  len(keys),
		Note:     "minted when the archive held no anchors of an existing set",
	})
	return id
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
