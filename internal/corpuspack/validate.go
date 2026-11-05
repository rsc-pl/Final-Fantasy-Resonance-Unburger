package corpuspack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var hex64 = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Stats is the validation result.
type Stats struct {
	Files           int
	Records         int
	JP              int
	Official        int
	RealEN          int
	MissingJP       int
	MissingOfficial int
	ByType          map[string]int
	ByReview        map[string]int
}

// Validate checks a corpus tree and refuses it on any structural defect: a duplicate identity, a missing
// record, a source text that drifted from its hash, or an editorial status nobody signed.
func Validate(dir string) (Stats, error) {
	st := Stats{ByType: map[string]int{}, ByReview: map[string]int{}}
	if err := refuseLegacy(dir); err != nil {
		return st, err
	}
	files, paths, err := loadWithPaths(dir)
	if err != nil {
		return st, err
	}
	seenID := map[string]string{}
	seenSource := map[string]string{}
	for i, f := range files {
		p := paths[i]
		if len(f.Records) == 0 {
			return st, fmt.Errorf("%s: file holds no records", p)
		}
		for _, r := range f.Records {
			st.Records++
			st.ByType[f.Group.Type]++
			if r.RecordID == "" {
				return st, fmt.Errorf("%s: record without id", p)
			}
			if prev, ok := seenID[r.RecordID]; ok {
				return st, fmt.Errorf("%s: duplicate record id %s (also in %s)", p, r.RecordID, prev)
			}
			seenID[r.RecordID] = p
			// The identity carries the discriminator of the original record as well, because a key can repeat
			// inside one namespace; the copies stay separate records.
			src := r.Source.Set + "|" + r.Source.Namespace + "|" + r.Source.Key + "|" + r.Source.JPRecordID + r.Source.OfficialENRecordID
			if prev, ok := seenSource[src]; ok {
				return st, fmt.Errorf("%s: duplicate source identity %s (also in %s)", p, src, prev)
			}
			seenSource[src] = p
			if r.Text.JP != nil {
				st.JP++
				if r.Source.JPSHA256 == "" {
					return st, fmt.Errorf("%s: %s has a Japanese text but no hash", p, r.RecordID)
				}
				if HashText(*r.Text.JP) != r.Source.JPSHA256 {
					return st, fmt.Errorf("%s: %s Japanese text does not match its hash", p, r.RecordID)
				}
			} else {
				st.MissingJP++
				if r.Source.JPSHA256 != "" {
					return st, fmt.Errorf("%s: %s has a Japanese hash but no text", p, r.RecordID)
				}
			}
			if r.Text.OfficialEN != nil {
				st.Official++
				if r.Source.OfficialENSHA256 == "" || HashText(*r.Text.OfficialEN) != r.Source.OfficialENSHA256 {
					return st, fmt.Errorf("%s: %s official English does not match its hash", p, r.RecordID)
				}
			} else {
				st.MissingOfficial++
				if r.Source.OfficialENSHA256 != "" {
					return st, fmt.Errorf("%s: %s has an official English hash but no text", p, r.RecordID)
				}
			}
			if r.Text.RealEN != nil {
				st.RealEN++
			}
			st.ByReview[r.Review.Status]++
			switch r.Review.Status {
			case StatusUnreviewed:
				if r.Review.Reason != nil {
					return st, fmt.Errorf("%s: %s is unreviewed but carries a decision", p, r.RecordID)
				}
			case StatusKeep, StatusChange, StatusHold:
				// The reason is optional metadata; the status itself is what the composition reads.
				if r.Text.RealEN == nil && r.Review.Status == StatusChange {
					return st, fmt.Errorf("%s: %s is a change without a real translation", p, r.RecordID)
				}
			default:
				return st, fmt.Errorf("%s: %s has unknown review status %q", p, r.RecordID, r.Review.Status)
			}
			if r.Source.JPSHA256 != "" && !hex64.MatchString(r.Source.JPSHA256) {
				return st, fmt.Errorf("%s: %s has a malformed Japanese hash", p, r.RecordID)
			}
		}
	}
	st.Files = len(files)
	return st, nil
}

// refuseLegacy rejects a tree that still holds the pre-migration monoliths or their directories.
func refuseLegacy(dir string) error {
	for _, legacy := range []string{"jp", "loc", "official-en", "en"} {
		if _, err := os.Stat(filepath.Join(dir, legacy)); err == nil {
			return fmt.Errorf("%s: legacy directory %q still present", dir, legacy)
		}
	}
	for _, name := range []string{"records.json", "jp.json", "en.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return fmt.Errorf("%s: legacy monolith %q still present", dir, name)
		}
	}
	return nil
}

// loadWithPaths reads every corpus file and returns it with its path.
func loadWithPaths(dir string) ([]File, []string, error) {
	var paths []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".json") || filepath.Base(p) == "manifest.json" {
			return nil
		}
		// The variant overlays are not corpus files: they hold overrides for records that live elsewhere.
		if strings.Contains(filepath.ToSlash(p), "/variants/") {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(paths)
	files := make([]File, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		var f File
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&f); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", p, err)
		}
		if f.SchemaVersion != SchemaVersion {
			return nil, nil, fmt.Errorf("%s: schema version %d, want %d", p, f.SchemaVersion, SchemaVersion)
		}
		files = append(files, f)
	}
	return files, paths, nil
}

// Index is the in-memory view the toolchain queries: records by id and by namespace, in file order.
type Index struct {
	Files []File
	ByID  map[string]Record
	ByNS  map[string][]Record
	Order []string
	Stats Stats
}

// LoadIndex reads a corpus tree once and builds the lookup the commands share.
func LoadIndex(dir string) (*Index, error) {
	files, _, err := loadWithPaths(dir)
	if err != nil {
		return nil, err
	}
	idx := &Index{Files: files, ByID: map[string]Record{}, ByNS: map[string][]Record{}, Stats: Stats{ByType: map[string]int{}, ByReview: map[string]int{}}}
	for _, f := range files {
		idx.Stats.Files++
		for _, r := range f.Records {
			idx.Stats.Records++
			idx.Stats.ByType[f.Group.Type]++
			idx.Stats.ByReview[r.Review.Status]++
			if r.Text.JP != nil {
				idx.Stats.JP++
			} else {
				idx.Stats.MissingJP++
			}
			if r.Text.OfficialEN != nil {
				idx.Stats.Official++
			} else {
				idx.Stats.MissingOfficial++
			}
			if r.Text.RealEN != nil {
				idx.Stats.RealEN++
			}
			idx.ByID[r.RecordID] = r
			idx.ByNS[r.Source.Namespace] = append(idx.ByNS[r.Source.Namespace], r)
			idx.Order = append(idx.Order, r.RecordID)
		}
	}
	return idx, nil
}
