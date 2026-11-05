package zen_test

import (
	"os"
	"strings"
	"testing"

	"unburger/internal/zen"
)

// TestKeyIdentities pins the per-key mapping and, just as important, the cases that must stay unresolved.
func TestKeyIdentities(t *testing.T) {
	for _, p := range []string{
		"../../.local/toolchain/records/dialogue-rows.json",
		"../../.local/toolchain/records/character-names.json",
	} {
		if _, err := os.Stat(p); err != nil {
			t.Skip("optional private source records not present on this host")
		}
	}
	idx := requireCanonicalCorpus(t)
	rows, err := zen.LoadDialogueRows("../../.local/toolchain/records/dialogue-rows.json")
	if err != nil {
		t.Fatal(err)
	}
	ids, err := zen.LoadIdentities("../../.local/toolchain/records/character-names.json")
	if err != nil {
		t.Fatal(err)
	}
	sets, err := zen.NamespaceKeySets(corpusMonolith(t, idx, "jp"))
	if err != nil {
		t.Fatal(err)
	}
	rep := zen.BuildKeyIdentities(rows, ids, sets)

	// The same key string occurs in more than one namespace, so a binding is scoped to (namespace, key).
	// 87 key strings are duplicated across ST_ namespaces and the record may never treat them as one row.
	if rep.PairsTotal != 14837 {
		t.Fatalf("pairs = %d, want 14837 (key,label) combinations", rep.PairsTotal)
	}
	if rep.Namespaces != 742 || rep.DialogueKeys != 14760 {
		t.Fatalf("namespaces/entries = %d/%d, want 742/14760", rep.Namespaces, rep.DialogueKeys)
	}
	if rep.KeysWithLabel != 14502 || rep.KeysUnresolvedNamespace != 358 {
		t.Fatalf("labelled/unresolved = %d/%d, want 14502/358", rep.KeysWithLabel, rep.KeysUnresolvedNamespace)
	}
	if rep.KeysIdentified != 13079 || rep.KeysAmbiguous != 33 || rep.KeysMissing != 230 || rep.KeysMarker != 1157 {
		t.Fatalf("identified/ambiguous/missing/marker = %d/%d/%d/%d, want 13079/33/230/1157",
			rep.KeysIdentified, rep.KeysAmbiguous, rep.KeysMissing, rep.KeysMarker)
	}
	if rep.KeyConflicts != 3 {
		t.Fatalf("conflict keys = %d, want 3", rep.KeyConflicts)
	}
	if rep.KeysIdentified+rep.KeysAmbiguous+rep.KeysMissing+rep.KeysMarker+rep.KeyConflicts != rep.KeysWithLabel {
		t.Fatalf("status buckets do not add up to the labelled keys")
	}
	// No binding may span two namespaces, and none may carry an identifier without a namespace.
	for _, b := range rep.Bindings {
		if b.Status == zen.BindingIdentified && b.Namespace == "" {
			t.Fatalf("binding %q is identified without a namespace", b.Key)
		}
	}
	byKey := map[string]zen.KeyBinding{}
	for _, b := range rep.Bindings {
		byKey[b.Key] = b
	}
	// Markers: neither is a character.
	markers := map[string]bool{}
	for _, b := range rep.Bindings {
		if b.Status == zen.BindingMarker {
			markers[b.Label] = true
			if b.NameEntry != "" {
				t.Fatalf("%q marker carries a name entry %q", b.Label, b.NameEntry)
			}
		}
	}
	if !markers[blankLabelForTest] || !markers[screenTextLabelForTest] {
		t.Fatalf("blank/screen-text markers missing: %v", markers)
	}
	// A conflict key keeps both labels and no identifier.
	for _, b := range rep.Bindings {
		if b.Status == zen.BindingConflict {
			if !strings.Contains(b.Label, " | ") || b.NameEntry != "" {
				t.Fatalf("conflict binding %+v collapsed its labels or took an identifier", b)
			}
			if !strings.Contains(b.Key, "FEV_Town_05Old_21") {
				t.Fatalf("unexpected conflict key %q", b.Key)
			}
		}
	}
	// Younger-version labels keep their own label and stay unresolved rather than inheriting the parent.
	for _, label := range []string{"レイン(子ども)", "ラスウェル(子ども)"} {
		found := false
		for _, b := range rep.Bindings {
			if b.Label == label {
				found = true
				if b.Status != zen.BindingMissing || b.NameEntry != "" {
					t.Fatalf("%q resolved to %+v, want unresolved with no entry", label, b)
				}
			}
		}
		if !found {
			t.Fatalf("%q does not appear as its own label", label)
		}
	}
}

const (
	blankLabelForTest      = "空白"
	screenTextLabelForTest = "テロップ"
)
