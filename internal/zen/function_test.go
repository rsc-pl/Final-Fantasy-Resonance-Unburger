package zen_test

import (
	"os"
	"testing"

	"unburger/internal/zen"
)

// TestTextFunctions pins the classification: only records that require a speaker may enter the speaker
// denominator, and a record whose applicability is undetermined enters neither the denominator nor the
// not-applicable count.
func TestTextFunctions(t *testing.T) {
	if _, err := os.Stat("../../.local/toolchain/records/key-identities.json"); err != nil {
		t.Skip("optional private key identities not present on this host")
	}
	sets, err := zen.NamespaceKeySets(corpusMonolith(t, requireCanonicalCorpus(t), "jp"))
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := zen.LoadBindings("../../.local/toolchain/records/key-identities.json")
	if err != nil {
		t.Fatal(err)
	}
	rep := zen.BuildFunctionReport(sets, bindings)

	if rep.Records != rep.Applicable+rep.NotApplicable+rep.Undetermined {
		t.Fatalf("records %d != applicable %d + not applicable %d + undetermined %d",
			rep.Records, rep.Applicable, rep.NotApplicable, rep.Undetermined)
	}
	if rep.Applicable != rep.Verified+rep.Unknown {
		t.Fatalf("applicable %d != verified %d + unknown %d", rep.Applicable, rep.Verified, rep.Unknown)
	}
	// Scene-like namespaces and the battle-AI namespace hold spoken lines, so they enter the denominator
	// with an unknown speaker rather than being left out.
	if rep.Applicable != 15716 || rep.Verified != 13342 || rep.Unknown != 2374 {
		t.Fatalf("applicable/verified/unknown = %d/%d/%d, want 15716/13342/2374",
			rep.Applicable, rep.Verified, rep.Unknown)
	}
	// Reading the records of the unclassified namespaces moved most of them into a named family: what is
	// left undetermined is the residue no family claims.
	if rep.NotApplicable != 9924 || rep.Undetermined != 1084 {
		t.Fatalf("not applicable/undetermined = %d/%d, want 9924/1084", rep.NotApplicable, rep.Undetermined)
	}
	// NOT_APPLICABLE must never enter the denominator.
	if rep.CoveragePct < 84.8 || rep.CoveragePct > 85.0 {
		t.Fatalf("speaker coverage of applicable = %.2f%%, want 84.9%%", rep.CoveragePct)
	}
	// Item and skill families are never spoken lines.
	for _, f := range rep.Functions {
		switch f.Function {
		case zen.FunctionItemName, zen.FunctionItemText, zen.FunctionSkillName, zen.FunctionSkillText,
			zen.FunctionUnitName, zen.FunctionUnitText, zen.FunctionCharacterName, zen.FunctionMapName:
			if f.Verified != 0 || f.Unknown != 0 {
				t.Fatalf("%s has %d verified / %d unknown speakers: a speaker does not apply",
					f.Function, f.Verified, f.Unknown)
			}
		}
	}
}
