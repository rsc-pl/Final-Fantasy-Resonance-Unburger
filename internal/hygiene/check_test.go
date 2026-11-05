package hygiene

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDetectMissingRoot(t *testing.T) {
	if len(Run(t.TempDir())) == 0 {
		t.Fatal("incomplete project accepted")
	}
}
func TestRejectPublicSymlink(t *testing.T) {
	r := t.TempDir()
	_ = os.MkdirAll(filepath.Join(r, "cmd"), 0755)
	_ = os.Symlink("/etc", filepath.Join(r, "cmd", "link"))
	if len(Run(r)) == 0 {
		t.Fatal("missing docs and unsafe symlink accepted")
	}
}

func TestIdentifierScope(t *testing.T) {
	// Built at run time so this file itself carries no literal that the strict
	// heuristic would flag.
	codec := []byte(`{"text":"` + "H" + `264 video settings"}`)
	strict := []*regexp.Regexp{regexp.MustCompile(`(?i)(?:^|\W)(?:h|g)[0-9]{3,}(?:\W|$)`)}
	if identifierExposed("translations/jp/records.json", codec, strict) {
		t.Fatal("authentic translation text rejected as a private identifier")
	}
	if !identifierExposed("internal/cache/cache.go", codec, strict) {
		t.Fatal("code path must keep the strict identifier check")
	}
	hostPath := []byte("/" + "home" + "/someone/project/file")
	if !identifierExposed("translations/jp/records.json", hostPath, nil) {
		t.Fatal("absolute host path accepted inside translations")
	}
}

func minimalWorkspace(t *testing.T, spec workspaceSpec) string {
	t.Helper()
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.local/\n"), 0644))
	must(os.MkdirAll(filepath.Join(root, ".local", "authority"), 0755))
	must(os.WriteFile(filepath.Join(root, ".local", ".unburger-project"), []byte("FFRS_UNBURGER_V1\n"), 0644))
	b, err := json.Marshal(spec)
	must(err)
	must(os.WriteFile(filepath.Join(root, ".local", "authority", "hygiene.json"), b, 0644))
	return root
}

// TestPrivateSpecDrivesAuthorityChecks proves the public scanner reads the document inventory from the private
// manifest instead of hard-coding the names, and still fails when a listed document is absent.
func TestPrivateSpecDrivesAuthorityChecks(t *testing.T) {
	root := minimalWorkspace(t, workspaceSpec{Authority: []string{"required.md"}})
	if err := os.WriteFile(filepath.Join(root, ".local", "authority", "required.md"), []byte("rule"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, err := range Run(root) {
		if strings.Contains(err.Error(), "missing active rule") {
			t.Fatalf("present rule reported missing: %v", err)
		}
	}
	if err := os.Remove(filepath.Join(root, ".local", "authority", "required.md")); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, err := range Run(root) {
		if strings.Contains(err.Error(), "missing active rule: required.md") {
			found = true
		}
	}
	if !found {
		t.Fatal("a listed authority document was not required")
	}
}

// TestPrivateSpecForbiddenPattern proves configured private patterns are enforced without being embedded in the
// public scanner.
func TestPrivateSpecForbiddenPattern(t *testing.T) {
	root := minimalWorkspace(t, workspaceSpec{ForbiddenPatterns: []string{`(?i)blocked-token`}})
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "x.go"), []byte("package internal\n\n// BLOCKED-TOKEN\n"), 0644); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, err := range Run(root) {
		if strings.Contains(err.Error(), "private/internal identifier exposed: internal/x.go") {
			found = true
		}
	}
	if !found {
		t.Fatal("configured private pattern was not enforced")
	}
}

// TestMissingScanRootReportedOnce checks that a missing public directory produces one scan error, not a silent
// pass and not a duplicate from both WalkDir and its callback.
func TestMissingScanRootReportedOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.local/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, err := range Run(root) {
		if strings.Contains(err.Error(), "scan cmd:") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("missing cmd directory reported %d times, want 1", n)
	}
}
