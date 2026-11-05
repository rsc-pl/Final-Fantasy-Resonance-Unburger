package zen_test

import (
	"testing"

	"unburger/internal/zen"
)

// TestUntranslatedCensus pins the census of aligned records whose English still carries the Japanese text.
// These are not strings to audit: they are gaps, and the count must not drift unnoticed.
func TestUntranslatedCensus(t *testing.T) {
	idx := requireCanonicalCorpus(t)
	jp := corpusMonolith(t, idx, "jp")
	loc := corpusMonolith(t, idx, "official")
	rep, err := zen.FindUntranslated(jp, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Only records with both a namespace and a key are joined; the remaining aligned records are never matched on
	// a bare key.
	if rep.Aligned != 80707 {
		t.Fatalf("aligned = %d, want 80707", rep.Aligned)
	}
	if rep.Untranslated != 2079 {
		t.Fatalf("untranslated = %d, want 2079", rep.Untranslated)
	}
	if rep.Percent < 2.56 || rep.Percent > 2.58 {
		t.Fatalf("percent = %.2f, want 2.57", rep.Percent)
	}
	if len(rep.ByNamespace) != 105 {
		t.Fatalf("namespaces = %d, want 105", len(rep.ByNamespace))
	}
	top := rep.ByNamespace[0]
	if top.Namespace != "ST_SkillDescription" || top.Records != 378 {
		t.Fatalf("largest bucket = %s/%d, want ST_SkillDescription/378", top.Namespace, top.Records)
	}
	if top.Function != zen.FunctionSkillText {
		t.Fatalf("skill description classified as %q", top.Function)
	}
	for _, e := range rep.Examples {
		if !zen.HasJapaneseScript(e.Text) {
			t.Fatalf("%s/%s: example carries no Japanese script", e.Namespace, e.Key)
		}
	}
}

// TestJapaneseScriptDetection keeps the detection honest: Latin text is never counted.
func TestJapaneseScriptDetection(t *testing.T) {
	cases := map[string]bool{
		"Restores 200 HP.": false,
		"HPを200回復する":       true,
		"カナダ":              true,
		"":                 false,
		"HP":               false,
	}
	for in, want := range cases {
		if got := zen.HasJapaneseScript(in); got != want {
			t.Fatalf("HasJapaneseScript(%q) = %v, want %v", in, got, want)
		}
	}
}
