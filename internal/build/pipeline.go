package build

// The build composes the effective English text for every entry of the game's localisation resource and writes
// a replacement resource. It never invents text: a record takes its real translation when one exists, and the
// official English otherwise. Nothing is written unless the result parses back to exactly what was composed.

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"unburger/internal/corpuspack"
	"unburger/internal/locres"
	"unburger/internal/version"
)

// Status values a build can report. Structural success and in-game success are separate facts.
const (
	StatePass      = "PASS"
	StateBlocked   = "BLOCKED"
	StateFailed    = "FAILED"
	RuntimeUnknown = "UNVERIFIED"
)

// Options names the inputs of one build.
type Options struct {
	CorpusDir string
	LocresIn  string
	OutDir    string
	Version   string
}

// Report is written next to the built resource.
type Report struct {
	State           string        `json:"state"`
	Runtime         string        `json:"runtime"`
	Entries         int           `json:"entries"`
	RealEN          int           `json:"records_with_real_translation"`
	OfficialEN      int           `json:"records_falling_back_to_official_en"`
	MissingInCorpus int           `json:"entries_without_corpus_record"`
	TextDrift       int           `json:"entries_whose_official_text_drifted"`
	Markers         int           `json:"version_markers_replaced"`
	Version         string        `json:"version"`
	Archives        []ArchiveInfo `json:"archives,omitempty"`
	Output          string        `json:"output,omitempty"`
	OutputSHA256    string        `json:"output_sha256,omitempty"`
	Reason          string        `json:"reason,omitempty"`
}

// Run composes and verifies one resource. A missing authentic source is reported as BLOCKED with the reason,
// not as a silent success, and an error is returned only when the build may not proceed at all.
func Run(ctx context.Context, opt Options) (Report, error) {
	rep := Report{State: StateBlocked, Runtime: RuntimeUnknown}
	if _, err := os.Stat(opt.LocresIn); err != nil {
		rep.Reason = "no authentic localisation resource: pass --locres with a resource taken from the verified build"
		return rep, nil
	}
	idx, err := corpuspack.LoadIndex(opt.CorpusDir)
	if err != nil {
		return rep, err
	}
	if err := version.Validate(opt.Version); err != nil {
		rep.State = StateFailed
		rep.Reason = "version: " + err.Error()
		return rep, nil
	}
	if st, err := os.Stat(opt.LocresIn); err == nil && st.IsDir() {
		// A directory holds the whole shipped set. Only the archives that actually change are written, and an
		// empty result means there is nothing to ship rather than a patch that pretends to be one.
		infos, err := ComposeArchivesWith(idx, opt.LocresIn, opt.OutDir, nil, opt.Version)
		if err != nil {
			return rep, err
		}
		rep.Archives = infos
		for _, a := range infos {
			rep.Markers += a.Markers
			if a.Language == LangEnglish {
				rep.Entries += a.Entries
				rep.RealEN += a.Changed
			}
		}
		if rep.RealEN == 0 {
			rep.State = StateNotReady
			rep.Reason = "no approved real translation differs from the official text: a patch would be byte-identical to the shipped build"
			return rep, nil
		}
		rep.State = StatePass
		rep.Output = opt.OutDir
		return rep, nil
	}
	src, err := locres.Read(opt.LocresIn)
	if err != nil {
		return rep, fmt.Errorf("%s: %w", opt.LocresIn, err)
	}
	out := locres.File{Version: src.Version, Entries: make([]locres.Entry, 0, len(src.Entries))}
	changed := 0
	for _, e := range src.Entries {
		rec, ok := lookup(idx, e.Namespace, e.Key)
		if !ok {
			rep.MissingInCorpus++
			out.Entries = append(out.Entries, e)
			continue
		}
		if real := deref(rec.Text.RealEN); real != "" {
			text, replaced := version.Replace(real, opt.Version)
			if text != e.Text {
				changed++
			}
			rep.Markers += replaced
			out.Entries = append(out.Entries, locres.Entry{Namespace: e.Namespace, Key: e.Key, Text: text,
				SourceHash: e.SourceHash, NamespaceHash: e.NamespaceHash, KeyHash: e.KeyHash})
			rep.RealEN++
			continue
		}
		if off := deref(rec.Text.OfficialEN); off != "" && off != e.Text {
			// The corpus and the resource disagree about the official text. That is drift, not a licence to
			// choose one, so the entry keeps what the resource holds and the drift is counted.
			rep.TextDrift++
		}
		out.Entries = append(out.Entries, e)
		rep.OfficialEN++
	}
	if changed == 0 {
		rep.State = StateNotReady
		rep.Reason = "no approved real translation differs from the official text: a patch would be byte-identical to the shipped build"
		return rep, nil
	}
	blob, err := locres.Write(out)
	if err != nil {
		return rep, err
	}
	if err := os.MkdirAll(opt.OutDir, 0o755); err != nil {
		return rep, err
	}
	target := filepath.Join(opt.OutDir, filepath.Base(opt.LocresIn))
	if err := os.WriteFile(target, blob, 0o644); err != nil {
		return rep, err
	}
	// Read the result back and compare it with what was composed: a writer that cannot reproduce its own input
	// is a failure, not a success with a caveat.
	back, err := locres.Read(target)
	if err != nil {
		return rep, fmt.Errorf("written resource does not parse: %w", err)
	}
	if len(back.Entries) != len(out.Entries) {
		rep.State = StateFailed
		rep.Reason = fmt.Sprintf("roundtrip changed the entry count: %d != %d", len(back.Entries), len(out.Entries))
		return rep, nil
	}
	for i := range out.Entries {
		if back.Entries[i] != out.Entries[i] {
			rep.State = StateFailed
			rep.Reason = fmt.Sprintf("roundtrip changed entry %d (%s/%s)", i, out.Entries[i].Namespace, out.Entries[i].Key)
			return rep, nil
		}
	}
	for _, e := range out.Entries {
		if version.Unresolved(e.Text) {
			rep.State = StateFailed
			rep.Reason = fmt.Sprintf("unresolved version marker in %s/%s after substitution", e.Namespace, e.Key)
			return rep, nil
		}
	}
	rep.State = StatePass
	rep.Version = opt.Version
	rep.Entries = len(out.Entries)
	rep.Output = target
	rep.OutputSHA256 = corpuspack.HashText(string(blob))
	return rep, nil
}

// lookup finds the corpus record for one localisation entry. The pair (namespace, key) is the only join; a key
// on its own is ambiguous by construction.
func lookup(idx *corpuspack.Index, namespace, key string) (corpuspack.Record, bool) {
	for _, r := range idx.ByNS[namespace] {
		if r.Source.Key == key {
			return r, true
		}
	}
	return corpuspack.Record{}, false
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// WriteReport writes the build report as JSON.
func WriteReport(path string, rep Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Package archives a built payload into a distributable zip with a manifest of what it contains.
func Package(outDir, distDir, name string) (string, error) {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return "", fmt.Errorf("nothing to package: %w", err)
	}
	var files []string
	payload := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if e.Name() == "build-report.json" || e.Name() == "package-manifest.json" {
			continue
		}
		files = append(files, e.Name())
		payload++
	}
	if payload == 0 {
		return "", fmt.Errorf("no built resource in %s: a blocked build cannot be packaged", outDir)
	}
	sort.Strings(files)
	man := map[string]interface{}{"name": name, "files": files}
	mb, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(outDir, "package-manifest.json"), append(mb, '\n'), 0o644); err != nil {
		return "", err
	}
	files = append(files, "package-manifest.json")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(distDir, name+".zip")
	return target, writeZip(target, outDir, files)
}

func writeZip(target, dir string, files []string) error {
	// Publish atomically: a partial archive must not remain at the final name if a read or write fails.
	f, err := os.CreateTemp(filepath.Dir(target), "ffrs-tmp-*.zip")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	cleanup := func() {
		f.Close()
		os.Remove(tmpPath)
	}
	zw := zip.NewWriter(f)
	for _, name := range files {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			cleanup()
			return err
		}
		w, err := zw.Create(name)
		if err != nil {
			cleanup()
			return err
		}
		if _, err := w.Write(b); err != nil {
			cleanup()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
