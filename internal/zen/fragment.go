package zen

import (
	"encoding/binary"
	"fmt"
)

// FFragment is one 16-bit fragment of an unversioned property header:
//
//	bits 0..6   SkipNum
//	bit  7      HasAnyZeroes
//	bit  8      IsLast
//	bits 9..15  ValueNum
//
// This is the layout used by UAssetAPI's FFragment.Unpack and by
// UnversionedPropertySerialization.cpp; the earlier reading of the low byte as SkipNum and the
// high byte as ValueNum was wrong and turned the single-fragment header 0x0300 into an
// impossible 3 + 63 property count.
type FFragment struct {
	SkipNum      uint16
	ValueNum     uint16
	HasAnyZeroes bool
	IsLast       bool
}

// UnpackFragment decodes a raw 16-bit fragment.
func UnpackFragment(raw uint16) FFragment {
	return FFragment{
		SkipNum:      raw & 0x007F,
		HasAnyZeroes: raw&0x0080 != 0,
		IsLast:       raw&0x0100 != 0,
		ValueNum:     raw >> 9,
	}
}

// UnversionedHeader is a parsed FUnversionedHeader: the fragments, the zero mask and the total
// number of serialized properties.
type UnversionedHeader struct {
	Fragments []FFragment
	ZeroMask  []bool
	// FirstNums holds the property index of each fragment's first value.
	FirstNums []int
	Values    int
	ZeroBits  int
	// Size is the number of bytes the header occupied.
	Size int
}

// ParseUnversionedHeader decodes a fragment stream and the optional zero mask.
func ParseUnversionedHeader(b []byte) (*UnversionedHeader, error) {
	if len(b) < 2 {
		return nil, fmt.Errorf("unversioned header: %d bytes", len(b))
	}
	h := &UnversionedHeader{}
	pos := 0
	first := 0
	for {
		if pos+2 > len(b) {
			return nil, fmt.Errorf("unversioned header: truncated fragment stream")
		}
		f := UnpackFragment(binary.LittleEndian.Uint16(b[pos:]))
		pos += 2
		h.Fragments = append(h.Fragments, f)
		h.FirstNums = append(h.FirstNums, first+int(f.SkipNum))
		first += int(f.SkipNum) + int(f.ValueNum)
		h.Values += int(f.ValueNum)
		if f.HasAnyZeroes {
			h.ZeroBits += int(f.ValueNum)
		}
		if len(h.Fragments) > 1<<16 {
			return nil, fmt.Errorf("unversioned header: fragment stream does not terminate")
		}
		if f.IsLast {
			break
		}
	}
	// CUE4Parse FUnversionedHeader: the zero mask is sized by the number of bits it carries — one
	// byte up to 8 bits, two bytes up to 16, and 32-bit words above that. Sizing it by words at every
	// width, or by bytes at every width, shifts every value in the row.
	switch {
	case h.ZeroBits == 0:
	case h.ZeroBits <= 8:
		if pos+1 > len(b) {
			return nil, fmt.Errorf("unversioned header: truncated zero mask")
		}
		h.ZeroMask = bitsToBools(b[pos], h.ZeroBits)
		pos++
	case h.ZeroBits <= 16:
		if pos+2 > len(b) {
			return nil, fmt.Errorf("unversioned header: truncated zero mask")
		}
		h.ZeroMask = bitsToBools16(binary.LittleEndian.Uint16(b[pos:]), h.ZeroBits)
		pos += 2
	default:
		words := (h.ZeroBits + 31) / 32
		if pos+words*4 > len(b) {
			return nil, fmt.Errorf("unversioned header: truncated zero mask")
		}
		h.ZeroMask = make([]bool, 0, h.ZeroBits)
		for w := 0; w < words; w++ {
			word := binary.LittleEndian.Uint32(b[pos:])
			pos += 4
			for bit := 0; bit < 32 && len(h.ZeroMask) < h.ZeroBits; bit++ {
				h.ZeroMask = append(h.ZeroMask, word&(1<<bit) != 0)
			}
		}
	}
	if len(h.ZeroMask) != 0 && len(h.ZeroMask) != h.ZeroBits {
		return nil, fmt.Errorf("unversioned header: zero mask has %d bits, want %d", len(h.ZeroMask), h.ZeroBits)
	}
	h.Size = pos
	return h, nil
}

func bitsToBools(v byte, n int) []bool {
	out := make([]bool, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, v&(1<<i) != 0)
	}
	return out
}

func bitsToBools16(v uint16, n int) []bool {
	out := make([]bool, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, v&(1<<i) != 0)
	}
	return out
}

// IsZero reports whether serialized value i (in fragment order) is present in the zero mask and
// therefore skipped on disk.
func (h *UnversionedHeader) IsZero(i int) bool {
	if i < 0 || i >= len(h.ZeroMask) {
		return false
	}
	return h.ZeroMask[i]
}
