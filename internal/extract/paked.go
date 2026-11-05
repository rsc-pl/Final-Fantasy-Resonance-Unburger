package extract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"unburger/internal/discovery"
	"unburger/internal/locres"
	"unburger/internal/oodle"
	"unburger/internal/pak"
)

// locresMagic marks an Unreal localisation archive payload.
var locresMagic = []byte{0x0e, 0x14, 0x74, 0x75, 0x67, 0x4a, 0x03, 0xfc, 0x4a, 0x15, 0x90, 0x9d, 0xc3, 0x37, 0x7f, 0x1b}

// OodleLibrary returns the first vendored Oodle library under .local/toolchain/vendor/oodle,
// or "" when no local library is present.
func OodleLibrary(root string) string {
	base := filepath.Join(root, ".local", "toolchain", "vendor", "oodle")
	for _, name := range []string{"liboo2corelinux64.so.9", "liboo2corelinux64.so"} {
		p := filepath.Join(base, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// Packed decodes localisation archives out of cooked .pak containers and writes one
// inventory per archive. Stored archives are always read; compressed archives need the
// vendored Oodle library and are reported, never silently skipped.
func Packed(root string, sources []discovery.Source) (int, int, error) {
	lib := OodleLibrary(root)
	var decode pak.Decoder
	if lib != "" {
		if err := oodle.Load(lib); err != nil {
			return 0, 0, err
		}
		decode = func(comp []byte, raw int) ([]byte, error) { return oodle.Decode(lib, comp, raw) }
	}
	written, skipped := 0, 0
	for _, src := range sources {
		for _, name := range src.Files {
			file := name
			if !filepath.IsAbs(file) {
				file = filepath.Join(src.ContainerDir, name)
			}
			if !strings.EqualFold(filepath.Ext(file), ".pak") {
				continue
			}
			r, err := pak.Open(file)
			if err != nil {
				return written, skipped, fmt.Errorf("%s: %w", file, err)
			}
			version := sourceVersion(src, file)
			for _, e := range r.Entries() {
				if e.BlockCount > 0 && decode == nil {
					skipped++
					continue
				}
				data, err := r.Read(e, decode)
				if err != nil || len(data) < 20 || !bytes.Equal(data[:16], locresMagic) {
					if err != nil {
						r.Close()
						return written, skipped, fmt.Errorf("%s entry %d: %w", file, e.Index, err)
					}
					continue
				}
				f, err := locres.Parse(data)
				if err != nil {
					r.Close()
					return written, skipped, fmt.Errorf("%s entry %d: %w", file, e.Index, err)
				}
				sum := sha256.Sum256(data)
				sha := hex.EncodeToString(sum[:])
				source := fmt.Sprintf("pak:%s#%d", filepath.Base(file), e.Index)
				if err := writeInventory(root, src.Platform, source, sha, f.Version, version, f.Entries); err != nil {
					r.Close()
					return written, skipped, err
				}
				written++
			}
			if err := r.Close(); err != nil {
				return written, skipped, err
			}
		}
	}
	return written, skipped, nil
}

// sourceVersion names the release a container came from, using the layer directory that
// discovery reported. It is provenance only: identity of a record never depends on it.
func sourceVersion(src discovery.Source, file string) string {
	rel, err := filepath.Rel(src.Root, file)
	if err != nil {
		rel = file
	}
	base := strings.ToLower(src.Root)
	if strings.Contains(base, "updates") || strings.Contains(filepath.ToSlash(rel), "updates") {
		return src.Platform + "-update"
	}
	return src.Platform + "-base"
}

// writeInventory stores one archive's records atomically.
func writeInventory(root, platform, source, sha string, locresVersion uint8, sourceVersion string, entries []locres.Entry) error {
	v := Inventory{Platform: platform, Source: source, SHA256: sha, Version: locresVersion, SourceVersion: sourceVersion, Entries: entries}
	out, err := marshalInventory(v)
	if err != nil {
		return err
	}
	layer := "base"
	if strings.HasSuffix(sourceVersion, "-update") {
		layer = "update"
	}
	dest := filepath.Join(root, ".local", "extract", "inventory", platform+"-"+layer+"-"+sha[:16]+".json")
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	stage, err := os.CreateTemp(filepath.Dir(dest), "ffrs-tmp-")
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
	return os.Rename(stage.Name(), dest)
}
