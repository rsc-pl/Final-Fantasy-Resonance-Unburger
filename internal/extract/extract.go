package extract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unburger/internal/locres"
)

type Inventory struct {
	Platform      string         `json:"platform"`
	Source        string         `json:"source"`
	SHA256        string         `json:"sha256"`
	Version       uint8          `json:"version"`
	SourceVersion string         `json:"source_version,omitempty"`
	Entries       []locres.Entry `json:"entries"`
}

// marshalInventory renders an inventory for disk.
func marshalInventory(v Inventory) ([]byte, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func Loose(root string) (int, error) {
	total := 0
	for _, platform := range []string{"switch", "pc"} {
		base := filepath.Join(root, ".local", "cache", "extracted", platform)
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, e error) error {
			if os.IsNotExist(e) {
				return nil
			}
			if e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink in extracted resources: %s", path)
			}
			if d.IsDir() {
				return nil
			}
			if !strings.EqualFold(filepath.Ext(path), ".locres") {
				return nil
			}
			f, e := locres.Read(path)
			if e != nil {
				return fmt.Errorf("%s: %w", path, e)
			}
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			sum := sha256.Sum256(b)
			h := hex.EncodeToString(sum[:])
			rel, e := filepath.Rel(base, path)
			if e != nil {
				return e
			}
			v := Inventory{Platform: platform, Source: filepath.ToSlash(rel), SHA256: h, Version: f.Version, Entries: f.Entries}
			out, e := json.MarshalIndent(v, "", "  ")
			if e != nil {
				return e
			}
			out = append(out, '\n')
			dest := filepath.Join(root, ".local", "extract", "inventory", platform+"-"+h[:16]+".json")
			if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
				return e
			}
			stage, e := os.CreateTemp(filepath.Dir(dest), "ffrs-tmp-")
			if e != nil {
				return e
			}
			defer os.Remove(stage.Name())
			if _, e = stage.Write(out); e != nil {
				stage.Close()
				return e
			}
			if e = stage.Close(); e != nil {
				return e
			}
			if e = os.Rename(stage.Name(), dest); e != nil {
				return e
			}
			total++
			return nil
		})
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
