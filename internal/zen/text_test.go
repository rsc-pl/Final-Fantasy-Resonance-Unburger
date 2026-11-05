package zen_test

import (
	"path/filepath"
	"strings"
	"testing"

	"unburger/internal/iostore"
	"unburger/internal/zen"
)

// TestReadFTextRealKey decodes an FText string-table entry from the real DT payload at the offset
// where the row's key is serialized, and requires the key to be one of the table's nine keys.
func TestReadFTextRealKey(t *testing.T) {
	paks, dec := fixture(t)
	c, err := iostore.Open(filepath.Join(paks, "FFRS-Switch.utoc"))
	if err != nil {
		t.Fatal(err)
	}
	var target iostore.File
	for _, f := range c.Files() {
		if strings.HasSuffix(f.Path, "DT_SubEv_C09_Gilgamesh_01.uasset") {
			target = f
			break
		}
	}
	data, err := c.ReadFile(target, dec)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := zen.ParsePackage(data, zen.HeaderSoftPackageReferencesOffs, "DT_SubEv_C09_Gilgamesh_01")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := pkg.ExportPayload(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	pos := 0x44
	txt, err := zen.ReadFText(payload, &pos)
	if err != nil {
		t.Fatalf("ftext at 0x44: %v", err)
	}
	if txt.HistoryType != zen.TextHistoryStringTable {
		t.Fatalf("history %d", txt.HistoryType)
	}
	if txt.Key != "SubEv_C09_Gilgamesh_01_001" {
		t.Fatalf("key %q", txt.Key)
	}
	if txt.TableIDIndex >= uint32(len(pkg.Names)) {
		t.Fatalf("table id index %d outside the name map", txt.TableIDIndex)
	}
	t.Logf("flags=%#x table=%q key=%q size=%d", txt.Flags, pkg.Names[txt.TableIDIndex], txt.Key, txt.Size)
}
