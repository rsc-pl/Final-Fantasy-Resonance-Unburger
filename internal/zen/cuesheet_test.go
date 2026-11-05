package zen_test

import (
	"os"
	"path/filepath"
	"testing"

	"unburger/internal/iostore"
	"unburger/internal/oodle"
	"unburger/internal/usmap"
	"unburger/internal/zen"
)

// TestCueSheetResolution is the regression for the script-import path: without the ScriptObjects table from
// global.utoc the event-voice tables' row struct cannot be named at all, and with it they decode exactly.
func TestCueSheetResolution(t *testing.T) {
	paks := "../../.local/originals/switch/updates/1.0.1/romfs/FFRS/Content/Paks"
	lib := "../../.local/toolchain/vendor/oodle/liboo2corelinux64.so.9"
	mapPath := "../../.local/toolchain/maps/FFRS-5.6.1-0+UE5-24b12662.usmap"
	for _, p := range []string{paks, lib, mapPath} {
		if _, err := os.Stat(p); err != nil {
			t.Skip("game data or mapping file not present on this host")
		}
	}
	if err := oodle.Load(lib); err != nil {
		t.Skip(err)
	}
	decode := func(b []byte, n int) ([]byte, error) { return oodle.Decode(lib, b, n) }
	m, err := usmap.Load(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := zen.CollectCueSheets(paks, decode, m)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(rec.Tables) != 11 || rec.RowTotal != 429 {
		t.Fatalf("cue-sheet tables/rows = %d/%d, want 11/429", len(rec.Tables), rec.RowTotal)
	}
	for _, tbl := range rec.Tables {
		if tbl.RowStruct != "SOUND_CUE_SHEET_ASSET" {
			t.Fatalf("%s resolved to %q, want SOUND_CUE_SHEET_ASSET", tbl.Asset, tbl.RowStruct)
		}
		if len(tbl.Rows) == 0 {
			t.Fatalf("%s decoded no rows", tbl.Asset)
		}
	}
	// The record names cue sheets only. It must not invent a speaker for any row.
	named := 0
	for _, tbl := range rec.Tables {
		for _, r := range tbl.Rows {
			if r.CueSheetName != "" {
				named++
			}
		}
	}
	if named == 0 {
		t.Fatal("no row carries a cue sheet name")
	}
	if _, err := iostore.Open(filepath.Join(paks, "FFRS-Switch.utoc")); err != nil {
		t.Fatalf("container: %v", err)
	}
}
