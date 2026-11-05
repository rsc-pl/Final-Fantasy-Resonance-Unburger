package ingest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func inv(entries ...[2]string) inventory {
	var out inventory
	out.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	for _, e := range entries {
		out.Entries = append(out.Entries, struct {
			Namespace  string `json:"namespace"`
			Key        string `json:"key"`
			Text       string `json:"text"`
			SourceHash uint32 `json:"source_hash"`
		}{Namespace: e[0], Key: e[1], Text: e[1]})
	}
	return out
}

func TestLanguageDetection(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"……なんだか不思議な気配がするな。", "ja"},
		{"離席", "zh"}, // Han-only short strings are ambiguous; a real archive always has kana
		{"What in the...? I can sense a strange presence here and you should look at the sky.", "en"},
		{"The blade is not ready and you should wait", "en"},
		{"부재", "ko"},
		{"离开", "zh"},
	}
	for _, c := range cases {
		got := language(inv([2]string{"", c.text}))
		if got != c.want {
			t.Fatalf("language(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestOverlapAndSignature(t *testing.T) {
	a := map[string]bool{"x\x00k1": true, "x\x00k2": true, "x\x00k3": true}
	b := map[string]bool{"x\x00k1": true, "x\x00k2": true}
	if o := overlap(a, b); o < 0.6 || o > 0.7 {
		t.Fatalf("overlap = %v, want ~0.66", o)
	}
	s1 := signature(inv([2]string{"ns", "k1"}, [2]string{"ns", "k2"}))
	s2 := signature(inv([2]string{"ns", "k2"}, [2]string{"ns", "k1"}))
	if s1 != s2 {
		t.Fatal("signature depends on record order")
	}
	s3 := signature(inv([2]string{"ns", "k1"}, [2]string{"ns", "k3"}))
	if s1 == s3 {
		t.Fatal("different key spaces share a signature")
	}
}

// TestRealInventoriesClassified documents what the classifier must produce on the real
// extraction, and stops the moment a language file drifts.
func TestRealInventoriesClassified(t *testing.T) {
	dir := filepath.Join(projectRoot(t), ".local", "extract", "inventory")
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Skip("no inventories on disk")
	}
	counts := map[string]int{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var v inventory
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		counts[language(v)]++
	}
	t.Logf("language histogram: %v", counts)
	if counts["ja"] == 0 || counts["en"] == 0 {
		t.Fatalf("classifier found no ja/en archives: %v", counts)
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}
