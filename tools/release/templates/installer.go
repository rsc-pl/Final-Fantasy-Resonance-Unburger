package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
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
type Op struct {
	rel     string
	old     []byte
	oldMode os.FileMode
	existed bool
	new     []byte
	mode    os.FileMode
	deleted bool
	backup  bool
}

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func allowed(p string) bool {
	if p == "README.md" || p == ".gitignore" || p == "go.mod" || p == "config/project.example.json" || p == ".local/.unburger-project" || p == ".local/config/project.json" {
		return true
	}
	for _, prefix := range []string{"cmd/", "internal/", "tools/", "schemas/", ".local/authority/", ".local/reference/baseline/"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	switch p {
	case "translations/manifest.json", "glossary/glossary.json", ".local/toolchain/records/platform_map.json", ".local/toolchain/records/source_ledger.json", ".local/toolchain/records/scenes.json", ".local/toolchain/records/speakers.json", ".local/toolchain/records/decisions.json", ".local/toolchain/records/review.json":
		return true
	}
	return false
}
func policyFor(p string) string {
	if p == ".local/config/project.json" || strings.HasPrefix(p, "translations/") || strings.HasPrefix(p, ".local/toolchain/records/") {
		return "seed"
	}
	return "managed"
}
func validatePath(p string) error {
	if p == "" || filepath.IsAbs(p) || filepath.Clean(p) != p || strings.Contains(p, "\\") || strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") || !allowed(p) {
		return fmt.Errorf("nonmanaged or unsafe path: %q", p)
	}
	return nil
}

// workspaceSpecPath is the private, Git-ignored workspace inventory. Directory initialization reads it when it
// exists in the envelope payload or the target tree; a public checkout without the file installs the release
// payload and creates no workspace directories.
const workspaceSpecPath = ".local/authority/hygiene.json"

type workspaceSpec struct {
	Directories []string `json:"directories"`
}

func validateDirPath(rel string) error {
	if rel == "" || filepath.IsAbs(rel) || filepath.Clean(rel) != rel || strings.Contains(rel, "\\") {
		return fmt.Errorf("unsafe workspace directory: %q", rel)
	}
	clean := filepath.Clean(filepath.Join(".local", filepath.FromSlash(rel)))
	if clean == ".local" || !strings.HasPrefix(clean, ".local"+string(filepath.Separator)) {
		return fmt.Errorf("workspace directory escapes .local: %q", rel)
	}
	return nil
}

func loadWorkspaceDirs(pkg, root string) ([]string, error) {
	var raw []byte
	var err error
	found := false
	for _, base := range []string{filepath.Join(pkg, "payload"), root} {
		raw, err = os.ReadFile(filepath.Join(base, filepath.FromSlash(workspaceSpecPath)))
		if err == nil {
			found = true
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if !found {
		return nil, nil
	}
	var spec workspaceSpec
	if err = json.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("workspace directory config: %w", err)
	}
	dirs := make([]string, 0, len(spec.Directories))
	seen := map[string]bool{}
	for _, rel := range spec.Directories {
		if err = validateDirPath(rel); err != nil {
			return nil, err
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		dirs = append(dirs, rel)
	}
	return dirs, nil
}
func checkTree(root, rel string) error {
	st, e := os.Lstat(root)
	if e == nil {
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return fmt.Errorf("unsafe root: %s", root)
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	cur := root
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		st, e := os.Lstat(cur)
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe symlink: %s", cur)
		}
		if i < len(parts)-1 && !st.IsDir() {
			return fmt.Errorf("not a directory: %s", cur)
		}
	}
	return nil
}
func writeAtomic(path string, b []byte, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), "ffrs-tmp-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Chmod(mode); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func load(pkg string) (Manifest, error) {
	m := Manifest{}
	b, e := os.ReadFile(filepath.Join(pkg, "manifest.json"))
	if e != nil {
		return m, e
	}
	if e = json.Unmarshal(b, &m); e != nil {
		return m, e
	}
	if m.ManifestVersion != 1 || m.Release == "" || len(m.Files) == 0 {
		return m, errors.New("unsupported/empty manifest")
	}
	return m, nil
}
func checkAncestors(root string) error {
	cur := filepath.Clean(root)
	for {
		st, e := os.Lstat(cur)
		if e == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in installation path: %s", cur)
		}
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	return nil
}
func doInstall(pkg, root string) error {
	if e := checkAncestors(root); e != nil {
		return e
	}
	if !filepath.IsAbs(pkg) || !filepath.IsAbs(root) {
		return errors.New("absolute arguments required")
	}
	st, e := os.Lstat(root)
	fresh := errors.Is(e, os.ErrNotExist)
	if e != nil && !fresh {
		return e
	}
	if !fresh && (st.Mode()&os.ModeSymlink != 0 || !st.IsDir()) {
		return errors.New("unsafe target root")
	}
	if !fresh {
		marker := filepath.Join(root, ".local", ".unburger-project")
		b, me := os.ReadFile(marker)
		if me != nil {
			ents, re := os.ReadDir(root)
			if re != nil {
				return re
			}
			if len(ents) != 0 {
				return errors.New("nonempty uninitialized target; refused")
			}
		}
		if me == nil && strings.TrimSpace(string(b)) != "FFRS_UNBURGER_V1" {
			return errors.New("target belongs to a different project")
		}
	}
	m, e := load(pkg)
	if e != nil {
		return e
	}
	dirs, e := loadWorkspaceDirs(pkg, root)
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	ops := []Op{}
	noop := 0
	for _, entry := range m.Files {
		if e = validatePath(entry.Path); e != nil {
			return e
		}
		if seen[entry.Path] {
			return fmt.Errorf("duplicate path %s", entry.Path)
		}
		seen[entry.Path] = true
		if entry.Policy != "managed" && entry.Policy != "seed" && entry.Policy != "remove" {
			return fmt.Errorf("unknown file policy: %s", entry.Path)
		}
		if (entry.Policy == "seed" && policyFor(entry.Path) != "seed") || (entry.Policy == "managed" && policyFor(entry.Path) != "managed") || (entry.Policy == "remove" && policyFor(entry.Path) != "managed") {
			return fmt.Errorf("illegal policy for path: %s", entry.Path)
		}
		if e = checkTree(root, entry.Path); e != nil {
			return e
		}
		dst := filepath.Join(root, entry.Path)
		old, readErr := os.ReadFile(dst)
		exists := readErr == nil
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		oldMode := os.FileMode(0644)
		if exists {
			s, se := os.Lstat(dst)
			if se != nil {
				return se
			}
			if !s.Mode().IsRegular() {
				return fmt.Errorf("not a file: %s", dst)
			}
			oldMode = s.Mode().Perm()
		}
		if entry.Policy == "remove" {
			if !exists {
				noop++
				continue
			}
			if !slices.Contains(entry.AcceptedPrevious, hash(old)) {
				return fmt.Errorf("unknown retirement edit: %s", entry.Path)
			}
			ops = append(ops, Op{rel: entry.Path, old: old, oldMode: oldMode, existed: true, deleted: true})
			continue
		}
		sourceRoot := filepath.Join(pkg, "payload")
		if e = checkTree(sourceRoot, entry.Path); e != nil {
			return e
		}
		src := filepath.Join(sourceRoot, entry.Path)
		st, se := os.Lstat(src)
		if se != nil {
			return se
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("unsafe payload file: %s", entry.Path)
		}
		b, se := os.ReadFile(src)
		if se != nil {
			return se
		}
		if hash(b) != entry.SHA256 {
			return fmt.Errorf("SHA mismatch: %s", entry.Path)
		}
		if entry.Policy == "seed" && exists {
			noop++
			continue
		}
		if exists && hash(old) == entry.SHA256 {
			noop++
			continue
		}
		backup := false
		if exists && !slices.Contains(entry.AcceptedPrevious, hash(old)) {
			if !strings.HasPrefix(entry.Path, ".local/authority/") || !strings.HasSuffix(entry.Path, ".md") {
				return fmt.Errorf("unknown edit; refused: %s", entry.Path)
			}
			backup = true
		}
		mode := os.FileMode(entry.Mode) & 0777
		if mode == 0 {
			mode = 0644
		}
		if mode&0111 != 0 && !strings.HasSuffix(entry.Path, ".sh") {
			return fmt.Errorf("unexpected executable: %s", entry.Path)
		}
		ops = append(ops, Op{rel: entry.Path, old: old, oldMode: oldMode, existed: exists, new: b, mode: mode, backup: backup})
	}
	if fresh {
		if e = os.MkdirAll(root, 0755); e != nil {
			return e
		}
	}
	backupRoot := ""
	for _, op := range ops {
		if op.backup {
			if backupRoot == "" {
				backupRoot = filepath.Join(root, ".local", "backups", "installer", time.Now().UTC().Format("20060102T150405.000000000"))
			}
			if e = writeAtomic(filepath.Join(backupRoot, op.rel), op.old, 0600); e != nil {
				return e
			}
		}
	}
	applied := []Op{}
	rollback := func(problem error) error {
		for i := len(applied) - 1; i >= 0; i-- {
			op := applied[i]
			dst := filepath.Join(root, op.rel)
			if op.existed {
				_ = writeAtomic(dst, op.old, op.oldMode)
			} else {
				_ = os.Remove(dst)
			}
		}
		return fmt.Errorf("installation rolled back after failure: %w", problem)
	}
	for _, op := range ops {
		if e = checkTree(root, op.rel); e != nil {
			return rollback(e)
		}
		dst := filepath.Join(root, op.rel)
		if op.deleted {
			e = os.Remove(dst)
		} else {
			e = writeAtomic(dst, op.new, op.mode)
		}
		if e != nil {
			return rollback(e)
		}
		applied = append(applied, op)
	}
	for _, rel := range dirs {
		rel = filepath.Join(".local", filepath.FromSlash(rel))
		if e = checkTree(root, rel); e != nil {
			return rollback(e)
		}
		if e = os.MkdirAll(filepath.Join(root, rel), 0700); e != nil {
			return rollback(e)
		}
	}
	fmt.Printf("Installed %s: %d changes, %d unchanged\n", m.Release, len(ops), noop)
	if backupRoot != "" {
		fmt.Printf("Edited authority saved: %s\n", backupRoot)
	}
	return nil
}
func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: installer.go <envelope> <project-root>")
		os.Exit(2)
	}
	pkg, e := filepath.Abs(os.Args[1])
	if e != nil {
		panic(e)
	}
	root, e := filepath.Abs(os.Args[2])
	if e != nil {
		panic(e)
	}
	if e = doInstall(pkg, root); e != nil {
		fmt.Fprintln(os.Stderr, "REFUSED:", e)
		os.Exit(1)
	}
}
