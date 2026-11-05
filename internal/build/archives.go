package build

// Choosing which localisation archives a patch may touch is an evidence question, not a guess: an archive is
// English when the text it carries matches the official English the corpus already holds, and Japanese when it
// matches the Japanese. An archive that matches neither is left alone, because rewriting it would be a change
// nobody asked for.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"unburger/internal/corpuspack"
	"unburger/internal/locres"
	"unburger/internal/version"
)

// Language of a localisation archive, decided from its content.
const (
	LangEnglish  = "english"
	LangJapanese = "japanese"
	LangUnknown  = "unknown"
)

// ArchiveInfo is what one archive turned out to be.
type ArchiveInfo struct {
	File       string `json:"file"`
	Entries    int    `json:"entries"`
	Language   string `json:"language"`
	Matched    int    `json:"matched"`
	Sampled    int    `json:"sampled"`
	Changed    int    `json:"changed"`
	Markers    int    `json:"version_markers"`
	Namespaces int    `json:"namespaces"`
}

// detectLanguage compares a sample of the archive with the corpus. It returns the language and the number of
// sampled entries that matched on that language.
func detectLanguage(f locres.File, idx *corpuspack.Index) (string, int, int) {
	sample := 200
	if len(f.Entries) < sample {
		sample = len(f.Entries)
	}
	step := 1
	if len(f.Entries) > sample {
		step = len(f.Entries) / sample
	}
	en, jp, n := 0, 0, 0
	for i := 0; i < len(f.Entries) && n < sample; i += step {
		e := f.Entries[i]
		rec, ok := lookup(idx, e.Namespace, e.Key)
		if !ok {
			continue
		}
		n++
		if rec.Text.OfficialEN != nil && *rec.Text.OfficialEN == e.Text {
			en++
		}
		if rec.Text.JP != nil && *rec.Text.JP == e.Text {
			jp++
		}
	}
	switch {
	case en > jp && en*2 >= n:
		return LangEnglish, en, n
	case jp > en && jp*2 >= n:
		return LangJapanese, jp, n
	default:
		return LangUnknown, maxInt(en, jp), n
	}
}

// ComposeArchives reads every archive in a directory, decides what it is and, for English archives, replaces the
// lines that carry an approved real translation. Everything else is copied byte for byte to the output, and an
// archive that would not change is not written at all: a patch that changes nothing must not look like one.
func ComposeArchives(idx *corpuspack.Index, locresDir, outDir string) ([]ArchiveInfo, error) {
	return ComposeArchivesWith(idx, locresDir, outDir, nil, "")
}

// TestChange is one line a private test build overrides. It is never a corpus record: it exists only to prove
// that a mounted layer is read by the game, and it is never written to the canonical RealTL.
type TestChange struct {
	Namespace string
	Key       string
	Text      string
}

// ComposeArchivesWith is ComposeArchives with an optional test change applied on top of the composition.
//
// The version is substituted after the canonical text and the variant have been chosen and before anything is
// written, so the same number reaches every archive and no file under translations/ is touched.
func ComposeArchivesWith(idx *corpuspack.Index, locresDir, outDir string, tc []TestChange, ver string) ([]ArchiveInfo, error) {
	paths, err := filepath.Glob(filepath.Join(locresDir, "*.locres"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var infos []ArchiveInfo
	for _, p := range paths {
		f, err := locres.Read(p)
		if err != nil {
			return infos, err
		}
		lang, matched, sampled := detectLanguage(f, idx)
		info := ArchiveInfo{File: filepath.Base(p), Entries: len(f.Entries), Language: lang, Matched: matched, Sampled: sampled}
		nsSet := map[string]bool{}
		for _, e := range f.Entries {
			nsSet[e.Namespace] = true
		}
		info.Namespaces = len(nsSet)
		if lang == LangEnglish {
			out := locres.File{Version: f.Version, Entries: make([]locres.Entry, 0, len(f.Entries))}
			for _, e := range f.Entries {
				hit := false
				for _, c := range tc {
					if e.Namespace == c.Namespace && e.Key == c.Key {
						text, replaced := version.Replace(c.Text, ver)
						info.Markers += replaced
						out.Entries = append(out.Entries, locres.Entry{Namespace: e.Namespace, Key: e.Key, Text: text,
							SourceHash: e.SourceHash, NamespaceHash: e.NamespaceHash, KeyHash: e.KeyHash})
						info.Changed++
						hit = true
						break
					}
				}
				if hit {
					continue
				}
				rec, ok := lookup(idx, e.Namespace, e.Key)
				if ok && rec.Text.RealEN != nil && *rec.Text.RealEN != "" && *rec.Text.RealEN != e.Text {
					text, replaced := version.Replace(*rec.Text.RealEN, ver)
					info.Markers += replaced
					out.Entries = append(out.Entries, locres.Entry{Namespace: e.Namespace, Key: e.Key, Text: text,
						SourceHash: e.SourceHash, NamespaceHash: e.NamespaceHash, KeyHash: e.KeyHash})
					info.Changed++
					continue
				}
				out.Entries = append(out.Entries, e)
			}
			for _, e := range out.Entries {
				if version.Unresolved(e.Text) {
					return infos, fmt.Errorf("unresolved version marker left in %s/%s (%s)", e.Namespace, e.Key, filepath.Base(p))
				}
			}
			if info.Changed > 0 {
				blob, err := locres.Write(out)
				if err != nil {
					return infos, err
				}
				if err := writeDirFile(outDir, filepath.Base(p), blob); err != nil {
					return infos, err
				}
			}
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// IsEnglish reports whether an archive may be rewritten by a patch.
func IsEnglish(lang string) bool { return lang == LangEnglish }

// writeDirFile writes a payload into a directory, creating it when needed.
func writeDirFile(dir, name string, blob []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), blob, 0o644)
}

// TrimArchiveName keeps archive names stable and filesystem safe.
func TrimArchiveName(s string) string {
	s = strings.ReplaceAll(s, string(filepath.Separator), "-")
	return strings.TrimSuffix(s, ".locres")
}
