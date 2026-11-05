package corpuspack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ExportOptions names what to export. A selector is a namespace, a file path, or a type; several selectors
// may be given and each record is written once.
type ExportOptions struct {
	Dir       string
	Namespace []string
	Type      string
	Out       string
}

// Export writes exactly the requested slice of the corpus, using the same record schema and the same source
// hashes, so what comes back can be checked against what went out. It never re-exports the whole corpus unless
// the selector asks for everything.
func Export(opt ExportOptions) (File, error) {
	idx, err := LoadIndex(opt.Dir)
	if err != nil {
		return File{}, err
	}
	want := map[string]bool{}
	for _, ns := range opt.Namespace {
		want[ns] = true
	}
	out := File{SchemaVersion: SchemaVersion}
	seen := map[string]bool{}
	for _, f := range idx.Files {
		if opt.Type != "" && f.Group.Type != opt.Type {
			continue
		}
		for _, r := range f.Records {
			if len(want) > 0 && !want[r.Source.Namespace] {
				continue
			}
			if seen[r.RecordID] {
				continue
			}
			seen[r.RecordID] = true
			out.Records = append(out.Records, r)
			if out.Group.Type == "" {
				out.Group = Group{Type: f.Group.Type, Namespace: f.Group.Namespace, SceneID: f.Group.SceneID, Chapter: f.Group.Chapter, OrderVerified: f.Group.OrderVerified}
			}
		}
	}
	if len(out.Records) == 0 {
		return File{}, fmt.Errorf("export selected no records")
	}
	if len(want) == 1 && out.Group.Namespace == "" {
		for ns := range want {
			out.Group.Namespace = ns
		}
	}
	blob, err := marshalIndent(out)
	if err != nil {
		return File{}, err
	}
	if err := os.MkdirAll(filepath.Dir(opt.Out), 0o755); err != nil {
		return File{}, err
	}
	if err := os.WriteFile(opt.Out, blob, 0o644); err != nil {
		return File{}, err
	}
	return out, nil
}

// ImportOptions configures an import of our own translation patch.
type ImportOptions struct {
	Dir     string
	Patch   string
	DryRun  bool
	History string
}

// ImportResult reports what an import did.
type ImportResult struct {
	Records  int
	Applied  int
	Skipped  int
	Files    []string
	Approval string
}

// Import applies real translations from a patch file. It refuses a patch whose source hashes do not match the
// corpus, refuses a decision that carries no approval, never touches the Japanese or official English text, and
// writes every touched file atomically after copying the previous state to the history directory.
func Import(opt ImportOptions) (ImportResult, error) {
	raw, err := os.ReadFile(opt.Patch)
	if err != nil {
		return ImportResult{}, err
	}
	var patch File
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&patch); err != nil {
		return ImportResult{}, fmt.Errorf("%s: %w", opt.Patch, err)
	}
	if patch.SchemaVersion != SchemaVersion {
		return ImportResult{}, fmt.Errorf("%s: schema version %d", opt.Patch, patch.SchemaVersion)
	}
	if len(patch.Records) == 0 {
		return ImportResult{}, fmt.Errorf("%s: patch holds no records", opt.Patch)
	}
	idx, err := LoadIndex(opt.Dir)
	if err != nil {
		return ImportResult{}, err
	}
	result := ImportResult{Records: len(patch.Records)}
	type pending struct {
		path string
		file File
	}
	touched := map[string]*pending{}
	approvalSeen := ""
	for _, pr := range patch.Records {
		if pr.Text.RealEN == nil {
			result.Skipped++
			continue
		}
		if pr.Review.Status == StatusUnreviewed {
			return ImportResult{}, fmt.Errorf("%s: a real translation must carry a review status, not UNREVIEWED", pr.RecordID)
		}
		if pr.Review.Reason == nil || strings.TrimSpace(*pr.Review.Reason) == "" {
			return ImportResult{}, fmt.Errorf("%s: review status without a reason", pr.RecordID)
		}
		cur, ok := idx.ByID[pr.RecordID]
		if !ok {
			return ImportResult{}, fmt.Errorf("%s: not in the corpus", pr.RecordID)
		}
		if pr.Source.JPSHA256 != "" && pr.Source.JPSHA256 != cur.Source.JPSHA256 {
			return ImportResult{}, fmt.Errorf("%s: Japanese source changed since export", pr.RecordID)
		}
		if pr.Source.OfficialENSHA256 != "" && pr.Source.OfficialENSHA256 != cur.Source.OfficialENSHA256 {
			return ImportResult{}, fmt.Errorf("%s: official English changed since export", pr.RecordID)
		}
		path, f, err := fileOf(opt.Dir, pr.RecordID)
		if err != nil {
			return ImportResult{}, err
		}
		t := touched[path]
		if t == nil {
			t = &pending{path: path, file: f}
			touched[path] = t
		}
		for i := range t.file.Records {
			rec := &t.file.Records[i]
			if rec.RecordID != pr.RecordID {
				continue
			}
			real := *pr.Text.RealEN
			rec.Text.RealEN = &real
			rec.Review.Status = pr.Review.Status
			rec.Review.Reason = pr.Review.Reason
			break
		}
		if pr.Review.Reason != nil {
			approvalSeen = *pr.Review.Reason
		}
		result.Applied++
	}
	result.Approval = approvalSeen
	if opt.DryRun || result.Applied == 0 {
		return result, nil
	}
	if opt.History != "" {
		dir := filepath.Join(opt.History, time.Now().UTC().Format("20060102T150405Z"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return ImportResult{}, err
		}
		for path := range touched {
			prev, err := os.ReadFile(path)
			if err != nil {
				return ImportResult{}, err
			}
			out := filepath.Join(dir, strings.ReplaceAll(filepath.ToSlash(path), "/", "__")+".prev")
			if err := os.WriteFile(out, prev, 0o644); err != nil {
				return ImportResult{}, err
			}
		}
	}
	paths := make([]string, 0, len(touched))
	for p := range touched {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		t := touched[p]
		blob, err := marshalIndent(t.file)
		if err != nil {
			return ImportResult{}, err
		}
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, blob, 0o644); err != nil {
			return ImportResult{}, err
		}
		if err := os.Rename(tmp, p); err != nil {
			return ImportResult{}, err
		}
		result.Files = append(result.Files, p)
	}
	if err := RefreshManifest(opt.Dir); err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

// fileOf finds the file that holds a record and returns it parsed.
func fileOf(dir, recordID string) (string, File, error) {
	paths := []string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".json") || filepath.Base(p) == "manifest.json" {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return "", File{}, err
	}
	sort.Strings(paths)
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return "", File{}, err
		}
		var f File
		if err := json.Unmarshal(raw, &f); err != nil {
			return "", File{}, fmt.Errorf("%s: %w", p, err)
		}
		for _, r := range f.Records {
			if r.RecordID == recordID {
				return p, f, nil
			}
		}
	}
	return "", File{}, fmt.Errorf("%s: not in the corpus", recordID)
}

// RefreshManifest recomputes the manifest from the files on disk, keeping it the only index of the corpus.
func RefreshManifest(dir string) error {
	files, paths, err := loadWithPaths(dir)
	if err != nil {
		return err
	}
	man := Manifest{
		SchemaVersion: SchemaVersion,
		Format:        FormatName,
		GeneratedBy:   "unburger corpus manifest",
		Counts:        ManifestCounts{ByType: map[string]int{}, ByReviewStatus: map[string]int{}},
	}
	for i, f := range files {
		rel, err := filepath.Rel(dir, paths[i])
		if err != nil {
			rel = paths[i]
		}
		raw, err := os.ReadFile(paths[i])
		if err != nil {
			return err
		}
		man.Files = append(man.Files, ManifestEntry{
			Path:      filepath.ToSlash(rel),
			Type:      f.Group.Type,
			Namespace: f.Group.Namespace,
			Part:      f.Group.Part,
			Records:   len(f.Records),
			SHA256:    HashText(string(raw)),
		})
		man.Counts.Files++
		man.Counts.Records += len(f.Records)
		man.Counts.ByType[f.Group.Type] += len(f.Records)
		for _, r := range f.Records {
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
	blob, err := marshalIndent(man)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), blob, 0o644)
}
