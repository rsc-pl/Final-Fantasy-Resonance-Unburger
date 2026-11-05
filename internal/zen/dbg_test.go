package zen_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unburger/internal/iostore"
	"unburger/internal/zen"
)

func dumpTable(t *testing.T, asset string, limit int) {
	paks, dec := fixture(t)
	c, _ := iostore.Open(filepath.Join(paks, "FFRS-Switch.utoc"))
	for _, f := range c.Files() {
		if !strings.HasSuffix(f.Path, asset+".uasset") {
			continue
		}
		data, _ := c.ReadFile(f, dec)
		pkg, err := zen.ParsePackage(data, zen.HeaderSoftPackageReferencesOffs, asset)
		if err != nil {
			t.Logf("%s: %v", asset, err)
			return
		}
		payload, _ := pkg.ExportPayload(data, 0)
		h, _ := zen.ParseUnversionedHeader(payload)
		pe := h.Size + 4
		n := binary.LittleEndian.Uint32(payload[pe+4:])
		nameIdx := binary.LittleEndian.Uint32(payload[pe+8:])
		start := pe + 8
		rh, err := zen.ParseUnversionedHeader(payload[start+8:])
		if err != nil {
			t.Logf("=== %s header: %v", asset, err)
			return
		}
		mask := ""
		ser := 0
		for _, z := range rh.ZeroMask {
			if z {
				mask += "1"
			} else {
				mask += "0"
				ser++
			}
		}
		t.Logf("=== %-28s payload=%4d nameCountWord=%3d rowNameIdx=%d row0header=%dB values=%d mask=%s serialised=%d",
			asset, len(payload), n, nameIdx, rh.Size, rh.Values, mask, ser)
		for i := 0; i < limit; i += 8 {
			e := i + 8
			if start+e > len(payload) {
				break
			}
			t.Logf("   +%2d: % x", i, payload[start+i:start+e])
		}
		return
	}
	t.Logf("=== %s not found", asset)
}

func TestDbgChatFraming(t *testing.T) {
	if os.Getenv("FFRS_LAYOUT_PROBE") == "" {
		t.Skip("set FFRS_LAYOUT_PROBE=1")
	}
	for _, a := range []string{"DT_SubEv_C09_Gilgamesh_01", "DT_INN", "DT_ATC03_TalkEvent"} {
		dumpTable(t, a, 88)
	}
}
