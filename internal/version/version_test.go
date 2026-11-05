package version

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFormat: only MAJOR.MINOR.PATCH is a version; everything else is refused.
func TestFormat(t *testing.T) {
	for _, ok := range []string{"0.1.0", "1.2.3", "10.20.30"} {
		if err := Validate(ok); err != nil {
			t.Fatalf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "v1.2.3", "1.2", "1.2.3.4", "1.2.3-beta", " 1.2.3", "1.2.3 ", "1.2.x", "a.b.c", "1.2.3\n1.2.4"} {
		if err := Validate(bad); err == nil {
			t.Fatalf("%q accepted as a version", bad)
		}
	}
}

// TestLoad: the file may end with one line ending and must not hold anything else.
func TestLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, FileName)
	if err := os.WriteFile(p, []byte("1.4.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := Load(p); err != nil || v != "1.4.2" {
		t.Fatalf("load = %q %v", v, err)
	}
	if err := os.WriteFile(p, []byte("1.4.2\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := Load(p); err != nil || v != "1.4.2" {
		t.Fatalf("crlf load = %q %v", v, err)
	}
	if err := os.WriteFile(p, []byte("v1.4.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("a v-prefixed version was accepted")
	}
	if _, err := Load(filepath.Join(dir, "absent.txt")); err == nil {
		t.Fatal("a missing version file was accepted")
	}
}

// TestReplaceEveryOccurrence: the marker is a literal token, every copy is replaced and nothing else changes.
func TestReplaceEveryOccurrence(t *testing.T) {
	text := "UNBURGER MOD v@@UNBURGER_VERSION@@\nsecond line also @@UNBURGER_VERSION@@ and @@UNBURGER_VERSION@@."
	out, n := Replace(text, "2.0.1")
	if n != 3 {
		t.Fatalf("replaced %d occurrences, want 3", n)
	}
	if strings.Contains(out, Marker) {
		t.Fatal("a marker survived the substitution")
	}
	if strings.Count(out, "2.0.1") != 3 {
		t.Fatalf("version occurrences = %d, want 3", strings.Count(out, "2.0.1"))
	}
	if !strings.HasPrefix(out, "UNBURGER MOD v2.0.1") {
		t.Fatalf("text changed unexpectedly: %q", out)
	}
	if _, n := Replace("no marker here", "1.0.0"); n != 0 {
		t.Fatal("a text without the marker reported a replacement")
	}
	if !Unresolved("still @@UNBURGER_VERSION@@") {
		t.Fatal("an unresolved marker was not reported")
	}
}
