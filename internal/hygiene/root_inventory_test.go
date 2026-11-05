package hygiene

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.local/\n*.staged\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func runGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func unexpectedRootErrors(errs []error) []string {
	var found []string
	for _, err := range errs {
		if strings.Contains(err.Error(), "unexpected public root entry:") {
			found = append(found, err.Error())
		}
	}
	return found
}

// TestRootInventoryHonorsIgnoreRules proves an untracked root file matched by .gitignore is not a public entry.
func TestRootInventoryHonorsIgnoreRules(t *testing.T) {
	root := initRepo(t)
	if err := os.WriteFile(filepath.Join(root, "scratch.staged"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if out := runGit(t, root, "ls-files", "--cached", "--others", "--exclude-standard"); strings.Contains(out, "scratch.staged") {
		t.Fatal("fixture file is still in the publishable set")
	}
	if found := unexpectedRootErrors(Run(root)); len(found) != 0 {
		t.Fatalf("ignored root file rejected: %v", found)
	}
}

// TestRootInventoryRejectsPublishableEntry proves a tracked or untracked-and-not-ignored root entry still fails.
func TestRootInventoryRejectsPublishableEntry(t *testing.T) {
	root := initRepo(t)
	if err := os.WriteFile(filepath.Join(root, "extra.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	found := unexpectedRootErrors(Run(root))
	if len(found) != 1 || found[0] != "unexpected public root entry: extra.txt" {
		t.Fatalf("publishable root entry result = %v, want exactly extra.txt", found)
	}
}

// TestRootInventoryKeepsTrackedEntryDespiteIgnore proves an ignore rule cannot excuse an already tracked file.
func TestRootInventoryKeepsTrackedEntryDespiteIgnore(t *testing.T) {
	root := initRepo(t)
	if err := os.WriteFile(filepath.Join(root, "tracked.staged"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "-f", "tracked.staged")
	if out := runGit(t, root, "ls-files", "--cached"); !strings.Contains(out, "tracked.staged") {
		t.Fatal("fixture file is not tracked")
	}
	found := unexpectedRootErrors(Run(root))
	if len(found) != 1 || found[0] != "unexpected public root entry: tracked.staged" {
		t.Fatalf("tracked ignored root entry result = %v, want exactly tracked.staged", found)
	}
}

// TestRootInventoryHonorsInfoExclude proves the repository-local exclude file participates in the scan.
func TestRootInventoryHonorsInfoExclude(t *testing.T) {
	root := initRepo(t)
	if err := os.WriteFile(filepath.Join(root, "local-note.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(root, ".git", "info", "exclude"), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("/local-note.txt\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "check-ignore", "-q", "--", "local-note.txt")
	if found := unexpectedRootErrors(Run(root)); len(found) != 0 {
		t.Fatalf("info/exclude root file rejected: %v", found)
	}
}

// TestRootInventoryWithoutCommits proves the publishable set is derived from the working tree before the
// first commit, while already ignored files remain outside it.
func TestRootInventoryWithoutCommits(t *testing.T) {
	root := initRepo(t)
	cmd := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD")
	if err := cmd.Run(); err == nil {
		t.Fatal("fixture unexpectedly has commits")
	}
	for name := range map[string]bool{"pending.staged": true, "extra.txt": true} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	found := unexpectedRootErrors(Run(root))
	if len(found) != 1 || found[0] != "unexpected public root entry: extra.txt" {
		t.Fatalf("no-commit root inventory result = %v, want exactly extra.txt", found)
	}
}

// TestRootInventoryFallbackWithoutGit proves a directory outside a work tree keeps the static root allowlist.
func TestRootInventoryFallbackWithoutGit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.local/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extra.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	found := unexpectedRootErrors(Run(root))
	if len(found) != 1 || found[0] != "unexpected public root entry: extra.txt" {
		t.Fatalf("non-git fallback result = %v, want exactly extra.txt", found)
	}
}

// TestRootInventoryReportsGitFailure proves an unexpected Git invocation failure becomes a scan error and
// does not silently produce an empty publishable set.
func TestRootInventoryReportsGitFailure(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	script := filepath.Join(bin, "git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'fatal: repository unavailable' >&2\nexit 128\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	found := false
	for _, err := range Run(root) {
		if strings.Contains(err.Error(), "git root inventory:") {
			found = true
		}
	}
	if !found {
		t.Fatal("git failure was not surfaced as a scan error")
	}
}

// TestPrivateWorkspaceValidationStillRuns proves the private inventory checks are unchanged by the root
// inventory correction.
func TestPrivateWorkspaceValidationStillRuns(t *testing.T) {
	root := minimalWorkspace(t, workspaceSpec{Authority: []string{"rule.md"}})
	if err := os.Remove(filepath.Join(root, ".local", ".unburger-project")); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, err := range Run(root) {
		if strings.Contains(err.Error(), "project identity missing") {
			found = true
		}
	}
	if !found {
		t.Fatal("private project marker validation was skipped")
	}
}

// TestPublicCheckoutWithoutPrivateWorkspace proves a tree with no private inventory still passes the
// generic public checks.
func TestPublicCheckoutWithoutPrivateWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.local/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cmd", "internal", "tools", "schemas", "config", "translations"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if errs := Run(root); len(errs) != 0 {
		t.Fatalf("public checkout reported errors: %v", errs)
	}
}
