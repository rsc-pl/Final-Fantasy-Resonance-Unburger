package zen

import (
	"os"
	"path/filepath"
	"testing"

	"unburger/internal/usmap"
)

// TestProbeTiling searches for the offset at which schema-driven rows consume the payload exactly.
// A correct row start is the only one that yields whole rows to the last byte, so a unique hit
// settles the row framing without guessing.
func TestProbeTiling(t *testing.T) {
	if os.Getenv("FFRS_LAYOUT_PROBE") == "" {
		t.Skip("set FFRS_LAYOUT_PROBE=1")
	}
	root := probeRoot(t)
	m, err := usmap.Load(filepath.Join(root, ".local/toolchain/maps/FFRS-5.6.1-0+UE5-24b12662.usmap"))
	if err != nil {
		t.Fatal(err)
	}
	props, err := m.Flatten("TalkEventRowData")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(root, ".local/work/probe/row.bin"))
	if err != nil {
		t.Skip("no payload")
	}
	hits := 0
	for s := 0; s < 200 && s < len(payload); s++ {
		rows, err := ParseSchemaRows(payload, s, props, m, nil)
		if err != nil || len(rows) == 0 {
			continue
		}
		last := rows[len(rows)-1]
		if last.End != len(payload) {
			continue
		}
		hits++
		sizes := make([]int, 0, 4)
		for i, r := range rows {
			if i > 3 {
				break
			}
			sizes = append(sizes, r.End-r.Start)
		}
		t.Logf("TILING start=%d rows=%d firstSizes=%v", s, len(rows), sizes)
	}
	if hits == 0 {
		t.Logf("no offset tiles the payload with %d schema properties", len(props))
	}
}
