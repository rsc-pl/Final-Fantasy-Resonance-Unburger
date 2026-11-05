package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unburger/internal/hygiene"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	p := t.TempDir()
	r := filepath.Join(t.TempDir(), "fresh")
	os.MkdirAll(filepath.Join(p, "payload"), 0755)
	return p, r
}
func manifestFixture(t *testing.T, p string, es ...Entry) {
	t.Helper()
	b, _ := json.Marshal(Manifest{ManifestVersion: 1, Release: "TEST", Files: es})
	if e := os.WriteFile(filepath.Join(p, "manifest.json"), b, 0644); e != nil {
		t.Fatal(e)
	}
}
func add(t *testing.T, p, rel, content, policy string) Entry {
	t.Helper()
	s := filepath.Join(p, "payload", rel)
	os.MkdirAll(filepath.Dir(s), 0755)
	os.WriteFile(s, []byte(content), 0644)
	return Entry{Path: rel, Policy: policy, SHA256: hash([]byte(content)), Mode: 0644}
}
func TestFreshAndSeedPreservation(t *testing.T) {
	p, r := fixture(t)
	m := add(t, p, ".local/.unburger-project", "FFRS_UNBURGER_V1\n", "managed")
	s := add(t, p, "translations/manifest.json", "[]\n", "seed")
	manifestFixture(t, p, m, s)
	if e := doInstall(p, r); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(r, "translations/manifest.json"), []byte("OWNER"), 0644)
	if e := doInstall(p, r); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(r, "translations/manifest.json"))
	if string(b) != "OWNER" {
		t.Fatal("overwrote translation")
	}
}
func TestUnknownCodeRefused(t *testing.T) {
	p, r := fixture(t)
	m := add(t, p, ".local/.unburger-project", "FFRS_UNBURGER_V1\n", "managed")
	x := add(t, p, "internal/code.go", "new", "managed")
	manifestFixture(t, p, m, x)
	if e := doInstall(p, r); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(r, "internal/code.go"), []byte("modified"), 0644)
	if e := doInstall(p, r); e == nil {
		t.Fatal("overwrote managed code")
	}
}
func TestAuthorityBackup(t *testing.T) {
	p, r := fixture(t)
	m := add(t, p, ".local/.unburger-project", "FFRS_UNBURGER_V1\n", "managed")
	x := add(t, p, ".local/authority/NOTES.md", "first", "managed")
	manifestFixture(t, p, m, x)
	if e := doInstall(p, r); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(r, x.Path), []byte("local custom"), 0644)
	if e := doInstall(p, r); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(filepath.Join(r, ".local/backups/installer"))
	if e != nil || len(entries) != 1 {
		t.Fatal("no backup", e)
	}
	b, _ := os.ReadFile(filepath.Join(r, ".local/backups/installer", entries[0].Name(), x.Path))
	if string(b) != "local custom" {
		t.Fatal("wrong backup")
	}
}
func configEntry(t *testing.T, p string, dirs ...string) Entry {
	t.Helper()
	b, err := json.Marshal(workspaceSpec{Directories: dirs})
	if err != nil {
		t.Fatal(err)
	}
	return add(t, p, workspaceSpecPath, string(b), "managed")
}

func publicSurface(t *testing.T, r string) {
	t.Helper()
	for _, d := range []string{"cmd", "internal", "tools", "schemas", "config", "translations"} {
		if err := os.MkdirAll(filepath.Join(r, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	gitignore := filepath.Join(r, ".gitignore")
	if _, err := os.Stat(gitignore); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(gitignore, []byte("/.local/\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfiguredWorkspaceDirectories(t *testing.T) {
	p, r := fixture(t)
	m := add(t, p, ".local/.unburger-project", "FFRS_UNBURGER_V1\n", "managed")
	c := configEntry(t, p, "workspace/evidence", "workspace/archive")
	manifestFixture(t, p, m, c)
	if e := doInstall(p, r); e != nil {
		t.Fatal(e)
	}
	for _, d := range []string{"workspace/evidence", "workspace/archive"} {
		st, e := os.Lstat(filepath.Join(r, ".local", d))
		if e != nil || !st.IsDir() {
			t.Fatalf("missing configured directory %s: %v", d, e)
		}
	}
	keep := filepath.Join(r, ".local", "workspace", "evidence", "keep.txt")
	if e := os.WriteFile(keep, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := doInstall(p, r); e != nil {
		t.Fatal(e)
	}
	if b, e := os.ReadFile(keep); e != nil || string(b) != "keep" {
		t.Fatalf("reinstall damaged workspace content: %v", e)
	}
	publicSurface(t, r)
	if errs := hygiene.Run(r); len(errs) != 0 {
		t.Fatalf("hygiene rejected installed workspace: %v", errs)
	}
}

func TestUpgradePreservesExistingWorkspace(t *testing.T) {
	p, r := fixture(t)
	existing := filepath.Join(r, ".local", "workspace", "evidence", "keep.txt")
	if e := os.MkdirAll(filepath.Dir(existing), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(r, ".local", ".unburger-project"), []byte("FFRS_UNBURGER_V1\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(existing, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	legacy := filepath.Join(r, ".local", "unlisted", "nested", "keep.txt")
	if e := os.MkdirAll(filepath.Dir(legacy), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(legacy, []byte("legacy"), 0600); e != nil {
		t.Fatal(e)
	}
	m := add(t, p, ".local/.unburger-project", "FFRS_UNBURGER_V1\n", "managed")
	c := configEntry(t, p, "workspace/evidence", "workspace/new")
	manifestFixture(t, p, m, c)
	if e := doInstall(p, r); e != nil {
		t.Fatal(e)
	}
	for path, want := range map[string]string{existing: "keep", legacy: "legacy"} {
		b, e := os.ReadFile(path)
		if e != nil || string(b) != want {
			t.Fatalf("existing content changed at %s: %v", path, e)
		}
	}
	if st, e := os.Lstat(filepath.Join(r, ".local", "workspace", "new")); e != nil || !st.IsDir() {
		t.Fatalf("new configured directory missing: %v", e)
	}
}

func TestInstallWithoutWorkspaceInventory(t *testing.T) {
	p, r := fixture(t)
	x := add(t, p, "internal/code.go", "package internal\n", "managed")
	g := add(t, p, ".gitignore", "/.local/\n", "managed")
	manifestFixture(t, p, x, g)
	if e := doInstall(p, r); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Lstat(filepath.Join(r, ".local")); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("private workspace created without inventory: %v", e)
	}
	publicSurface(t, r)
	if errs := hygiene.Run(r); len(errs) != 0 {
		t.Fatalf("public checkout hygiene rejected install: %v", errs)
	}
}

func TestRejectUnsafeWorkspaceDirectories(t *testing.T) {
	for i, dir := range []string{"", "/absolute/path", "../escape", "workspace/../../escape", "workspace\\escape", "./workspace", "workspace//nested", "."} {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			p := t.TempDir()
			parent := t.TempDir()
			r := filepath.Join(parent, "root")
			if e := os.MkdirAll(filepath.Join(p, "payload"), 0755); e != nil {
				t.Fatal(e)
			}
			m := add(t, p, ".local/.unburger-project", "FFRS_UNBURGER_V1\n", "managed")
			c := configEntry(t, p, dir)
			manifestFixture(t, p, m, c)
			if e := doInstall(p, r); e == nil {
				t.Fatalf("unsafe workspace directory accepted: %q", dir)
			}
			if _, e := os.Lstat(r); !errors.Is(e, os.ErrNotExist) {
				t.Fatalf("target created before config rejection: %v", e)
			}
			if _, e := os.Lstat(filepath.Join(parent, "escape")); !errors.Is(e, os.ErrNotExist) {
				t.Fatalf("config traversal created a path outside .local: %v", e)
			}
		})
	}
}

func TestPrivateWorkspaceInventoryDirectories(t *testing.T) {
	specPath := filepath.Join("..", "..", "..", filepath.FromSlash(workspaceSpecPath))
	raw, err := os.ReadFile(specPath)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("private workspace inventory absent; public checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	var spec workspaceSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Directories) == 0 {
		t.Fatal("private workspace inventory has no directories")
	}
	p, r := fixture(t)
	m := add(t, p, ".local/.unburger-project", "FFRS_UNBURGER_V1\n", "managed")
	c := add(t, p, workspaceSpecPath, string(raw), "managed")
	manifestFixture(t, p, m, c)
	if e := doInstall(p, r); e != nil {
		t.Fatal(e)
	}
	for _, d := range spec.Directories {
		base := filepath.Join(r, ".local", filepath.FromSlash(d))
		st, e := os.Lstat(base)
		if e != nil || !st.IsDir() {
			t.Fatalf("configured directory %s missing: %v", d, e)
		}
		if e := os.WriteFile(filepath.Join(base, "keep.txt"), []byte("keep"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := doInstall(p, r); e != nil {
		t.Fatal(e)
	}
	for _, d := range spec.Directories {
		b, e := os.ReadFile(filepath.Join(r, ".local", filepath.FromSlash(d), "keep.txt"))
		if e != nil || string(b) != "keep" {
			t.Fatalf("reinstall changed content in %s: %v", d, e)
		}
	}
}

func TestTraversalSymlinkAndNonempty(t *testing.T) {
	if validatePath("../bad") == nil || validatePath(".local/rom/private.iso") == nil || validatePath("/etc/passwd") == nil {
		t.Fatal("unsafe path accepted")
	}
	p, r := fixture(t)
	os.MkdirAll(r, 0755)
	os.WriteFile(filepath.Join(r, "owned"), []byte("no"), 0644)
	m := add(t, p, ".local/.unburger-project", "FFRS_UNBURGER_V1\n", "managed")
	manifestFixture(t, p, m)
	if e := doInstall(p, r); e == nil || !strings.Contains(e.Error(), "nonempty") {
		t.Fatalf("unsafe root accepted: %v", e)
	}
}
