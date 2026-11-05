package zen

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"unburger/internal/usmap"
)

// fragmentsOf decodes the FFragment chain of an unversioned header, exactly as UAssetAPI does: each
// fragment is a uint16 with SkipNum in bits 0-6, HasAnyZeroes in bit 7, IsLast in bit 8 and ValueNum in
// bits 9-15; fragments are read until one has IsLast, and the zero mask follows.
func fragmentsOf(b []byte, pos int) (frags []Fragment, maskBytes []byte, total int, err error) {
	p := pos
	for {
		if p+2 > len(b) {
			return nil, nil, 0, fmt.Errorf("truncated fragment at %d", p)
		}
		raw := binary.LittleEndian.Uint16(b[p:])
		f := Fragment{
			SkipNum:       int(raw & 0x7F),
			HasAnyZeroes:  raw&0x80 != 0,
			IsLast:        raw&0x100 != 0,
			ValueNum:      int(raw >> 9),
			Raw:           raw,
			OffsetInBytes: p - pos,
		}
		frags = append(frags, f)
		p += 2
		total += f.ValueNum
		if f.IsLast {
			break
		}
		if len(frags) > 64 {
			return nil, nil, 0, fmt.Errorf("fragment chain does not terminate")
		}
	}
	hasZeros := false
	for _, f := range frags {
		if f.HasAnyZeroes {
			hasZeros = true
		}
	}
	words := 0
	if hasZeros {
		// The mask is serialised in 32-bit words, one bit per value that belongs to a fragment with
		// HasAnyZeroes.
		words = (total + 31) / 32
	}
	n := words * 4
	if p+n > len(b) {
		return nil, nil, 0, fmt.Errorf("truncated zero mask: need %d bytes at %d", n, p)
	}
	return frags, b[p : p+n], p + n - pos, nil
}

// Fragment is one decoded FFragment.
type Fragment struct {
	SkipNum       int
	HasAnyZeroes  bool
	IsLast        bool
	ValueNum      int
	Raw           uint16
	OffsetInBytes int
}

// TestProbeHeader prints the whole unversioned header of two rows and the explicit
// fragment -> schema index -> mask bit -> serialised mapping.
func TestProbeHeader(t *testing.T) {
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
	for _, base := range []int{14, 121} {
		t.Logf("=== row at %d", base)
		name := payload[base : base+8]
		t.Logf("RowName bytes: % x", name)
		frags, mask, size, err := fragmentsOf(payload, base+8)
		if err != nil {
			t.Logf("  header error: %v", err)
			continue
		}
		t.Logf("  header bytes: % x  (size %d)", payload[base+8:base+8+size], size)
		for i, f := range frags {
			t.Logf("  fragment %d: raw=%#06x at +%d skip=%d values=%d zeros=%v last=%v",
				i, f.Raw, f.OffsetInBytes, f.SkipNum, f.ValueNum, f.HasAnyZeroes, f.IsLast)
		}
		t.Logf("  mask bytes: % x", mask)
		// Build the explicit mapping: value ordinal -> schema index.
		idx := 0
		ord := 0
		for _, f := range frags {
			idx += f.SkipNum
			for v := 0; v < f.ValueNum; v++ {
				bit := -1
				if f.HasAnyZeroes {
					bit = ord % 32
				}
				zero := false
				if bit >= 0 && bit/8 < len(mask) {
					zero = mask[bit/8]&(1<<uint(bit%8)) != 0
				}
				label := "?"
				if idx < len(props) {
					label = props[idx].Name
				}
				t.Logf("    ord=%2d schemaIdx=%2d %-22s maskBit=%2d zero=%v serialised=%v",
					ord, idx, label, bit, zero, !zero && bit >= 0)
				idx++
				ord++
			}
			// Skipped properties contribute a skip but no value ordinal.
		}
	}
}
