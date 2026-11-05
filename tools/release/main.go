package main

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unburger/internal/hygiene"
)

type Entry struct {
	Path             string   `json:"path"`
	SHA256           string   `json:"sha256"`
	AcceptedPrevious []string `json:"accepted_previous"`
	Mode             uint32   `json:"mode"`
	Policy           string   `json:"policy"`
}
type Manifest struct {
	ManifestVersion int     `json:"manifest_version"`
	Release         string  `json:"release"`
	Files           []Entry `json:"files"`
}

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func managed(p string) bool {
	switch p {
	case "README.md", "go.mod", ".gitignore", "config/project.example.json", ".local/config/project.json", ".local/.unburger-project", "translations/manifest.json", "glossary/glossary.json", ".local/toolchain/records/platform_map.json", ".local/toolchain/records/source_ledger.json", ".local/toolchain/records/scenes.json", ".local/toolchain/records/speakers.json", ".local/toolchain/records/decisions.json", ".local/toolchain/records/review.json":
		return true
	}
	for _, prefix := range []string{"cmd/", "internal/", "tools/", "schemas/", ".local/authority/", ".local/reference/baseline/"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}
func policy(p string) string {
	if p == ".local/config/project.json" || strings.HasPrefix(p, "translations/") || strings.HasPrefix(p, ".local/toolchain/records/") {
		return "seed"
	}
	return "managed"
}
func fail(e error) { fmt.Fprintln(os.Stderr, "REFUSED:", e); os.Exit(1) }
func main() {
	out := flag.String("out", "dist/FFRS-UNBURGER-v1.tar.zst", "output archive")
	previous := flag.String("previous", "", "previous cumulative manifest")
	release := flag.String("release", "FFRS-UNBURGER-v1", "release identifier")
	flag.Parse()
	root, e := os.Getwd()
	if e != nil {
		fail(e)
	}
	if _, e = os.Stat(filepath.Join(root, ".local", ".unburger-project")); e != nil {
		fail(e)
	}
	if errs := hygiene.Run(root); len(errs) > 0 {
		for _, problem := range errs {
			fmt.Fprintln(os.Stderr, "HYGIENE FAIL:", problem)
		}
		fail(fmt.Errorf("release blocked by hygiene"))
	}
	fmt.Fprintln(os.Stderr, "PRIVATE WORKSPACE ENVELOPE: this archive carries .local/ workspace, authority, configuration and ledger records; it is not a public mod release and must never be published as one.")
	payload := map[string][]byte{}
	for _, prefix := range []string{"README.md", "go.mod", ".gitignore", "config", "cmd", "internal", "tools", "schemas", "translations", ".local/authority", ".local/reference/baseline", ".local/config/project.json", ".local/.unburger-project", ".local/toolchain/records"} {
		p := filepath.Join(root, prefix)
		st, e := os.Lstat(p)
		if e != nil {
			fail(e)
		}
		if st.Mode().IsRegular() {
			b, e := os.ReadFile(p)
			if e != nil {
				fail(e)
			}
			if managed(prefix) {
				payload[filepath.ToSlash(prefix)] = b
			}
			continue
		}
		if !st.IsDir() {
			fail(fmt.Errorf("unsafe release path %s", prefix))
		}
		e = filepath.WalkDir(p, func(full string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink in release tree: %s", full)
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, full)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if !managed(rel) {
				return nil
			}
			b, err := os.ReadFile(full)
			if err != nil {
				return err
			}
			payload[rel] = b
			return nil
		})
		if e != nil {
			fail(e)
		}
	}
	prev := map[string]Entry{}
	if *previous != "" {
		b, e := os.ReadFile(*previous)
		if e != nil {
			fail(e)
		}
		var m Manifest
		if e = json.Unmarshal(b, &m); e != nil || m.ManifestVersion != 1 {
			fail(fmt.Errorf("invalid earlier manifest: %v", e))
		}
		for _, ent := range m.Files {
			if !managed(ent.Path) {
				fail(fmt.Errorf("old manifest unexpected path: %s", ent.Path))
			}
			prev[ent.Path] = ent
		}
	}
	keys := make([]string, 0, len(payload))
	for p := range payload {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	m := Manifest{ManifestVersion: 1, Release: *release, Files: []Entry{}}
	for _, p := range keys {
		entry := Entry{Path: p, SHA256: sha(payload[p]), Mode: 0644, Policy: policy(p), AcceptedPrevious: []string{}}
		if strings.HasSuffix(p, ".sh") {
			entry.Mode = 0755
		}
		if old, found := prev[p]; found {
			entry.AcceptedPrevious = append(entry.AcceptedPrevious, old.AcceptedPrevious...)
			if old.SHA256 != entry.SHA256 && !slices.Contains(entry.AcceptedPrevious, old.SHA256) {
				entry.AcceptedPrevious = append(entry.AcceptedPrevious, old.SHA256)
			}
			slices.Sort(entry.AcceptedPrevious)
		}
		m.Files = append(m.Files, entry)
	}
	for p, old := range prev {
		if _, ok := payload[p]; ok || old.Policy == "remove" {
			continue
		}
		if policy(p) == "seed" || strings.HasPrefix(p, ".local/reference/baseline/") {
			continue
		}
		accepted := append([]string{}, old.AcceptedPrevious...)
		accepted = append(accepted, old.SHA256)
		slices.Sort(accepted)
		m.Files = append(m.Files, Entry{Path: p, AcceptedPrevious: accepted, Policy: "remove"})
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	manifest, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		fail(e)
	}
	manifest = append(manifest, '\n')
	env := "ffrs-unburger-v1"
	extra := map[string][]byte{env + "/manifest.json": manifest}
	for _, name := range []string{"INSTALL.sh", "installer.go"} {
		b, e := os.ReadFile(filepath.Join(root, "tools", "release", "templates", name))
		if e != nil {
			fail(e)
		}
		extra[env+"/"+name] = b
	}
	for p, b := range payload {
		extra[env+"/payload/"+p] = b
	}
	names := make([]string, 0, len(extra))
	for p := range extra {
		names = append(names, p)
	}
	sort.Strings(names)
	output, e := filepath.Abs(*out)
	if e != nil {
		fail(e)
	}
	if e = os.MkdirAll(filepath.Dir(output), 0755); e != nil {
		fail(e)
	}
	f, e := os.CreateTemp(filepath.Dir(output), "ffrs-tmp-*.tar")
	if e != nil {
		fail(e)
	}
	defer os.Remove(f.Name())
	tw := tar.NewWriter(f)
	for _, name := range names {
		b := extra[name]
		mode := int64(0644)
		if strings.HasSuffix(name, "/INSTALL.sh") {
			mode = 0755
		}
		header := &tar.Header{Name: name, Mode: mode, Typeflag: tar.TypeReg, Size: int64(len(b)), ModTime: time.Unix(0, 0).UTC()}
		if e = tw.WriteHeader(header); e != nil {
			fail(e)
		}
		if _, e = tw.Write(b); e != nil {
			fail(e)
		}
	}
	if e = tw.Close(); e != nil {
		fail(e)
	}
	if e = f.Close(); e != nil {
		fail(e)
	}
	staged := output + ".staged"
	defer os.Remove(staged)
	cmd := exec.Command("zstd", "-q", "-f", "-10", "-o", staged, f.Name())
	cmd.Stderr = os.Stderr
	if e = cmd.Run(); e != nil {
		fail(e)
	}
	if e = os.Rename(staged, output); e != nil {
		fail(e)
	}
	r, e := os.Open(output)
	if e != nil {
		fail(e)
	}
	defer r.Close()
	h := sha256.New()
	if _, e = io.Copy(h, r); e != nil {
		fail(e)
	}
	fmt.Printf("PACKAGE %s\nSHA256 %s\nMANAGED %d\n", output, hex.EncodeToString(h.Sum(nil)), len(keys))
}
