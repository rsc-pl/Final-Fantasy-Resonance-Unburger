package zen

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"unburger/internal/usmap"
)

// TestProbeRowLayout is a white-box probe, not an assertion: it prints which start offset and mask
// polarity make a row decode with the schema and end exactly where the next row begins.
func TestProbeRowLayout(t *testing.T) {
	if os.Getenv("FFRS_LAYOUT_PROBE") == "" {
		t.Skip("set FFRS_LAYOUT_PROBE=1")
	}
	dir, _ := os.Getwd()
	var root string
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			root = dir
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod")
		}
		dir = parent
	}
	m, err := usmap.Load(filepath.Join(root, ".local/toolchain/maps/FFRS-5.6.1-0+UE5-24b12662.usmap"))
	if err != nil {
		t.Fatal(err)
	}
	props, err := m.Flatten("TalkEventRowData")
	if err != nil {
		t.Fatal(err)
	}
	payload := probePayload(t)
	rowStart, rowEnd := 14, 121
	for s := rowStart + 8; s < rowEnd; s++ {
		h, err := ParseUnversionedHeader(payload[s:])
		if err != nil || len(h.ZeroMask) != len(props) {
			continue
		}
		for _, inverted := range []bool{false, true} {
			present := make([]bool, len(props))
			for i := range props {
				present[i] = h.ZeroMask[i] != inverted
			}
			pos := s + h.Size
			ok := true
			fields := 0
			for i, p := range props {
				if !present[i] {
					continue
				}
				r := &schemaReader{b: payload, pos: pos, m: m}
				var v FieldValue
				if err := r.readValue(p, &v); err != nil {
					ok = false
					break
				}
				pos = r.pos
				fields++
			}
			if ok && pos == rowEnd {
				t.Logf("START s=%d (row offset %d) inverted=%v fields=%d end=%d", s, s-rowStart, inverted, fields, pos)
			}
		}
	}
}

// probePayload loads one DataTable payload for the probe.
func probePayload(t *testing.T) []byte {
	t.Helper()
	root := probeRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".local/work/probe/row.bin"))
	if err != nil {
		t.Skip("no probe payload; run the unburger datatables dump first")
	}
	return data
}

func probeRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod")
		}
		dir = parent
	}
}

var _ = binary.LittleEndian
