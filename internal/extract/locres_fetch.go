package extract

// Read-only retrieval of the shipped localisation archives. It walks the physical entries of the .pak containers
// — the same walk the reader already uses, with no name index involved — recognises a localisation archive by
// its 16-byte signature, decodes it and writes one file per distinct payload under .local/work. Nothing here
// writes to a container, replaces an original, or needs a key.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"unburger/internal/locres"
	"unburger/internal/oodle"
	"unburger/internal/pak"
)

// LocresMagic is the signature every Unreal localisation archive starts with.
var LocresMagic = []byte{0x0e, 0x14, 0x74, 0x75, 0x67, 0x4a, 0x03, 0xfc, 0x4a, 0x15, 0x90, 0x9d, 0xc3, 0x37, 0x7f, 0x1b}

// FoundArchive is one distinct localisation payload found inside a container.
type FoundArchive struct {
	SHA256     string   `json:"sha256"`
	Short      string   `json:"short"`
	Path       string   `json:"path"`
	Layer      string   `json:"layer"`
	Container  string   `json:"container"`
	Entry      int      `json:"entry"`
	Compressed bool     `json:"compressed"`
	Version    uint8    `json:"version"`
	Entries    int      `json:"entries"`
	Namespaces int      `json:"namespaces"`
	Sample     []string `json:"sample_namespaces"`
}

// FetchReport summarises a retrieval.
type FetchReport struct {
	Containers int            `json:"containers"`
	Scanned    int            `json:"entries_scanned"`
	Found      int            `json:"localisation_entries"`
	Distinct   int            `json:"distinct_payloads"`
	Archives   []FoundArchive `json:"archives"`
	Reason     string         `json:"reason,omitempty"`
}

// Container names one .pak and the layer it comes from.
type Container struct {
	Path  string
	Layer string
}

// FetchAllLocres walks every container, in the order given, and writes each distinct localisation payload to
// outDir. A later container (an update) keeps its layer, so the effective resource set can be chosen by evidence
// rather than by assumption.
func FetchAllLocres(root string, containers []Container, outDir string) (FetchReport, error) {
	rep := FetchReport{}
	lib := OodleLibrary(root)
	var decode pak.Decoder
	if lib != "" {
		if err := oodle.Load(lib); err != nil {
			return rep, err
		}
		decode = func(comp []byte, raw int) ([]byte, error) { return oodle.Decode(lib, comp, raw) }
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return rep, err
	}
	seen := map[string]bool{}
	for _, c := range containers {
		r, err := pak.Open(c.Path)
		if err != nil {
			rep.Reason = fmt.Sprintf("%s: %v", c.Path, err)
			continue
		}
		rep.Containers++
		for _, e := range r.Entries() {
			rep.Scanned++
			compressed := e.BlockCount > 0
			if compressed && decode == nil {
				continue
			}
			data, err := r.Read(e, decode)
			if err != nil || len(data) < 20 || !bytes.Equal(data[:16], LocresMagic) {
				continue
			}
			rep.Found++
			sum := sha256.Sum256(data)
			sha := hex.EncodeToString(sum[:])
			if seen[sha] {
				continue
			}
			seen[sha] = true
			f, perr := locres.Parse(data)
			if perr != nil {
				r.Close()
				return rep, fmt.Errorf("%s entry %d: %w", c.Path, e.Index, perr)
			}
			target := filepath.Join(outDir, sha[:16]+".locres")
			if err := os.WriteFile(target, data, 0o644); err != nil {
				r.Close()
				return rep, err
			}
			nsSet := map[string]bool{}
			for _, en := range f.Entries {
				nsSet[en.Namespace] = true
			}
			ns := make([]string, 0, len(nsSet))
			for k := range nsSet {
				ns = append(ns, k)
			}
			sort.Strings(ns)
			sample := ns
			if len(sample) > 5 {
				sample = sample[:5]
			}
			rep.Archives = append(rep.Archives, FoundArchive{
				SHA256: sha, Short: sha[:16], Path: target, Layer: c.Layer,
				Container: filepath.Base(c.Path), Entry: e.Index, Compressed: compressed,
				Version: f.Version, Entries: len(f.Entries), Namespaces: len(ns), Sample: sample,
			})
		}
		r.Close()
	}
	rep.Distinct = len(rep.Archives)
	sort.Slice(rep.Archives, func(i, j int) bool {
		if rep.Archives[i].Layer != rep.Archives[j].Layer {
			return rep.Archives[i].Layer < rep.Archives[j].Layer
		}
		return rep.Archives[i].Short < rep.Archives[j].Short
	})
	return rep, nil
}

// WriteFetchReport stores the retrieval index next to the payloads.
func WriteFetchReport(path string, rep FetchReport) error {
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ProbeLocres reports the entry count and version of a localisation archive.
func ProbeLocres(path string) (int, uint8, error) {
	f, err := locres.Read(path)
	if err != nil {
		return 0, 0, err
	}
	return len(f.Entries), f.Version, nil
}
