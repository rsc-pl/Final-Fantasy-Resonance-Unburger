package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unburger/internal/discovery"
)

type File struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified_unix_ns"`
	SHA256   string `json:"sha256"`
}
type Manifest struct {
	Schema       int    `json:"schema"`
	Platform     string `json:"platform"`
	Root         string `json:"root"`
	ContainerDir string `json:"container_dir"`
	Files        []File `json:"files"`
	Mode         string `json:"mode"`
}

func digest(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func filename(s discovery.Source) string {
	h := sha256.Sum256([]byte(s.Platform + "\x00" + s.ContainerDir))
	return s.Platform + "-" + hex.EncodeToString(h[:8]) + ".json"
}
func Index(root string, s discovery.Source, force bool) (Manifest, int, error) {
	dest := filepath.Join(root, ".local", "cache", "manifests", filename(s))
	old := Manifest{}
	if b, e := os.ReadFile(dest); e == nil {
		_ = json.Unmarshal(b, &old)
	}
	known := map[string]File{}
	if old.ContainerDir == s.ContainerDir && old.Schema == 1 {
		for _, f := range old.Files {
			known[f.Name] = f
		}
	}
	m := Manifest{Schema: 1, Platform: s.Platform, Root: s.Root, ContainerDir: s.ContainerDir, Mode: "source-index-only; unpacked text is not yet available"}
	hits := 0
	for _, name := range s.Files {
		if filepath.Base(name) != name || strings.Contains(name, "..") {
			return m, hits, fmt.Errorf("unsafe filename %q", name)
		}
		p := filepath.Join(s.ContainerDir, name)
		st, e := os.Lstat(p)
		if e != nil {
			return m, hits, e
		}
		if !st.Mode().IsRegular() {
			return m, hits, fmt.Errorf("not a regular source: %s", p)
		}
		f := File{Name: name, Size: st.Size(), Modified: st.ModTime().UnixNano()}
		if o, ok := known[name]; ok && !force && o.Size == f.Size && o.Modified == f.Modified && len(o.SHA256) == 64 {
			f.SHA256 = o.SHA256
			hits++
		} else {
			f.SHA256, e = digest(p)
			if e != nil {
				return m, hits, e
			}
		}
		m.Files = append(m.Files, f)
	}
	b, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return m, hits, e
	}
	b = append(b, '\n')
	if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
		return m, hits, e
	}
	tmp, e := os.CreateTemp(filepath.Dir(dest), "ffrs-tmp-")
	if e != nil {
		return m, hits, e
	}
	defer os.Remove(tmp.Name())
	if _, e = tmp.Write(b); e != nil {
		tmp.Close()
		return m, hits, e
	}
	if e = tmp.Sync(); e != nil {
		tmp.Close()
		return m, hits, e
	}
	if e = tmp.Close(); e != nil {
		return m, hits, e
	}
	if e = os.Rename(tmp.Name(), dest); e != nil {
		return m, hits, e
	}
	return m, hits, nil
}
func Cleanup(root string, apply bool) ([]string, error) {
	dir := filepath.Join(root, ".local", "tmp", "generated")
	ents, e := os.ReadDir(dir)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	removed := []string{}
	for _, ent := range ents {
		n := ent.Name()
		if !strings.HasPrefix(n, "ffrs-tmp-") || ent.IsDir() || ent.Type()&os.ModeSymlink != 0 {
			continue
		}
		p := filepath.Join(dir, n)
		st, e := os.Lstat(p)
		if e != nil {
			return removed, e
		}
		if time.Since(st.ModTime()) < 24*time.Hour {
			continue
		}
		removed = append(removed, p)
		if apply {
			if e = os.Remove(p); e != nil {
				return removed, e
			}
		}
	}
	return removed, nil
}
