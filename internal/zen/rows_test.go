package zen_test

import (
	"path/filepath"
	"strings"
	"testing"

	"unburger/internal/iostore"
	"unburger/internal/zen"
)

func tablePayload(t *testing.T, asset string) (*zen.Package, []byte, *zen.DataTablePrologue) {
	t.Helper()
	paks, dec := fixture(t)
	c, err := iostore.Open(filepath.Join(paks, "FFRS-Switch.utoc"))
	if err != nil {
		t.Fatal(err)
	}
	var target iostore.File
	for _, f := range c.Files() {
		if strings.HasSuffix(f.Path, asset+".uasset") {
			target = f
			break
		}
	}
	if target.Path == "" {
		t.Fatalf("%s not found", asset)
	}
	data, err := c.ReadFile(target, dec)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := zen.ParsePackage(data, zen.HeaderSoftPackageReferencesOffs, asset)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := pkg.ExportPayload(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	pro, err := zen.ParseDataTablePrologue(payload, func(pi zen.PackageIndex) (string, bool) {
		return pkg.ResolvePackageIndex(pi, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	return pkg, payload, pro
}

// TestRowsGilgamesh01 requires nine rows that tile the payload exactly, in key order, each with a
// speaker label at the fixed position used by this struct.
func TestRowsGilgamesh01(t *testing.T) {
	pkg, payload, pro := tablePayload(t, "DT_SubEv_C09_Gilgamesh_01")
	rows, err := zen.ParseTableRows(pkg, payload, pro.DataOffset)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 9 {
		t.Fatalf("%d rows, want 9", len(rows))
	}
	wantKeys := []string{"_001", "_002", "_003", "_004", "_005", "_006", "_007", "_008", "_009"}
	wantLabels := []string{"レイン", "ラスウェル", "レイン", "テロップ", "レイン", "テロップ", "ラスウェル", "テロップ", "テロップ"}
	for i, r := range rows {
		if !strings.HasSuffix(r.Key, wantKeys[i]) {
			t.Fatalf("row %d key %q, want suffix %s", i, r.Key, wantKeys[i])
		}
		if r.Label != wantLabels[i] {
			t.Fatalf("row %d label %q, want %q (offsets %d..%d)", i, r.Label, wantLabels[i], r.Start, r.End)
		}
		if r.KeyOff < 0 || r.LabelOff < 0 {
			t.Fatalf("row %d missing key (%d) or label (%d) offset", i, r.KeyOff, r.LabelOff)
		}
	}
	if rows[len(rows)-1].End != len(payload) {
		t.Fatalf("rows end at %d, payload %d", rows[len(rows)-1].End, len(payload))
	}
}

// TestRowsGilgamesh03 is the independent second scene: a different number of rows, a different
// speaker order and a third label that does not appear in the first scene.
func TestRowsGilgamesh03(t *testing.T) {
	pkg, payload, pro := tablePayload(t, "DT_SubEv_C09_Gilgamesh_03")
	rows, err := zen.ParseTableRows(pkg, payload, pro.DataOffset)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"レイン", "ギルガメッシュ", "ギルガメッシュ", "ギルガメッシュ", "ギルガメッシュ", "ギルガメッシュ", "ラスウェル", "レイン", "ラスウェル", "レイン"}
	if len(rows) != len(want) {
		t.Fatalf("%d rows, want %d", len(rows), len(want))
	}
	for i := range want {
		if rows[i].Label != want[i] {
			t.Fatalf("row %d label %q, want %q", i, rows[i].Label, want[i])
		}
		if !strings.HasSuffix(rows[i].Key, "_0"+string(rune('1'+i/10))+string(rune('0'+i%10))) && len(rows[i].Key) == 0 {
			t.Fatalf("row %d has no key", i)
		}
	}
	if rows[len(rows)-1].End != len(payload) {
		t.Fatalf("rows end at %d, payload %d", rows[len(rows)-1].End, len(payload))
	}
}

// TestRowsTileIndependentTables checks that the walk consumes whole payloads on tables from
// scenes that have nothing to do with the two confirmed ones, and reports the row-size spread.
func TestRowsTileIndependentTables(t *testing.T) {
	assets := []string{
		"DT_12Arms_WeaponEnhance", "DT_Arena_RankBattle", "DT_Arena_RankOrderst",
		"DT_C01_001_02_TalkEvent", "DT_C01_001_05B_TalkEvent", "DT_C01_002_01_TalkEvent",
		"DT_C01_005_01_B_TalkEvent", "DT_C01_006_01_B_TalkEvent",
	}
	for _, asset := range assets {
		pkg, payload, pro := tablePayload(t, asset)
		rows, err := zen.ParseTableRows(pkg, payload, pro.DataOffset)
		if err != nil {
			t.Fatalf("%s: %v", asset, err)
		}
		if rows[len(rows)-1].End != len(payload) {
			t.Fatalf("%s: rows end at %d, payload %d", asset, rows[len(rows)-1].End, len(payload))
		}
		first, last := rows[0].Start, rows[0].End
		minLen, maxLen := last-first, last-first
		for _, r := range rows {
			n := r.End - r.Start
			if n < minLen {
				minLen = n
			}
			if n > maxLen {
				maxLen = n
			}
			if r.Index > 0 && r.Start != rows[r.Index-1].End {
				t.Fatalf("%s: gap before row %d", asset, r.Index)
			}
		}
		t.Logf("%-30s rows=%3d payload=%5d rowlen=%d..%d firstKey=%s", asset, len(rows), len(payload), minLen, maxLen, rows[0].Key)
	}
}

// TestRowsRefusesSubMinimumRow pins the guard that refuses a table whose walk produces a row shorter
// than an FName plus a header. DT_ATC30_TalkEvent used to be reported with a 4-byte "row" whose name
// was a dialogue key; it must be refused instead of counted.
func TestRowsRefusesSubMinimumRow(t *testing.T) {
	pkg, payload, pro := tablePayload(t, "DT_ATC30_TalkEvent")
	if _, err := zen.ParseTableRows(pkg, payload, pro.DataOffset); err == nil {
		t.Fatal("DT_ATC30_TalkEvent parsed, want refusal: its first row is shorter than a name")
	}
}

// TestTalkRowStructHasTenProperties proves the property count from the cooked data alone: every row
// of every TalkEventRowData table carries a zero mask whose length is the struct's live property
// count, so six unrelated tables agreeing on 10 pins NumProperties without the binary.
func TestTalkRowStructHasTenProperties(t *testing.T) {
	assets := []string{
		"DT_SubEv_C09_Gilgamesh_01", "DT_SubEv_C09_Gilgamesh_02", "DT_SubEv_C09_Gilgamesh_03",
		"DT_Arena_RankBattle", "DT_C01_002_01_TalkEvent", "DT_12Arms_WeaponEnhance",
	}
	checked := 0
	for _, a := range assets {
		pkg, payload, pro := tablePayload(t, a)
		rows, err := zen.ParseTableRows(pkg, payload, pro.DataOffset)
		if err != nil {
			t.Fatalf("%s: %v", a, err)
		}
		for _, r := range rows {
			h, err := zen.ParseUnversionedHeader(payload[r.Start+8:])
			if err != nil {
				t.Fatalf("%s row %d: %v", a, r.Index, err)
			}
			if len(h.ZeroMask) != 10 {
				t.Fatalf("%s row %d: zero mask length %d, want 10", a, r.Index, len(h.ZeroMask))
			}
			checked++
		}
	}
	if checked < 70 {
		t.Fatalf("only %d rows checked", checked)
	}
	t.Logf("checked %d rows across %d tables, all with 10 properties", checked, len(assets))
}
