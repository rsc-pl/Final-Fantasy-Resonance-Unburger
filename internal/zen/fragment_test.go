package zen

import (
	"encoding/binary"
	"testing"
)

// TestFragment0300 pins the layout the whole row parse rests on: 0x0300 is one fragment with
// SkipNum 0, one value, IsLast set and no zero mask.
func TestFragment0300(t *testing.T) {
	f := UnpackFragment(0x0300)
	if f.SkipNum != 0 || f.ValueNum != 1 || !f.IsLast || f.HasAnyZeroes {
		t.Fatalf("0x0300 decoded as %+v", f)
	}
	h, err := ParseUnversionedHeader([]byte{0x00, 0x03})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Fragments) != 1 || h.Values != 1 || h.ZeroBits != 0 || h.Size != 2 {
		t.Fatalf("header %+v", h)
	}
}

// TestFragmentMultiCoversSkipZeroMask checks a multi-fragment header with a zero mask.
func TestFragmentMultiCoversSkipZeroMask(t *testing.T) {
	// fragment 1: skip 2, 3 values, zeroes present, not last  -> 0x0682
	// fragment 2: skip 1, 2 values, last                     -> 0x0501
	// three bits of zero mask take one byte
	buf := []byte{0x82, 0x06, 0x01, 0x05, 0x03}
	h, err := ParseUnversionedHeader(buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Fragments) != 2 || h.Values != 5 || h.ZeroBits != 3 {
		t.Fatalf("header %+v", h)
	}
	if h.FirstNums[0] != 2 || h.FirstNums[1] != 6 {
		t.Fatalf("first nums %v", h.FirstNums)
	}
	if len(h.ZeroMask) != 3 || !h.ZeroMask[0] || !h.ZeroMask[1] || h.ZeroMask[2] {
		t.Fatalf("zero mask %v", h.ZeroMask)
	}
	if h.Size != 5 {
		t.Fatalf("size %d", h.Size)
	}
}

// TestFragmentRejectsBadBounds requires hard errors for truncated input and a mask that does
// not match the declared bit count.
func TestFragmentRejectsBadBounds(t *testing.T) {
	if _, err := ParseUnversionedHeader(nil); err == nil {
		t.Fatal("accepted empty input")
	}
	if _, err := ParseUnversionedHeader([]byte{0x02}); err == nil {
		t.Fatal("accepted a truncated fragment")
	}
	// never-terminating stream
	if _, err := ParseUnversionedHeader([]byte{0x00, 0x00, 0x00, 0x00}); err == nil {
		t.Fatal("accepted a stream without IsLast")
	}
	// zero mask declared but missing: fragment with 1 value and zeroes, not last, then last
	buf := []byte{0x80, 0x02, 0x00, 0x03}
	_ = binary.LittleEndian.Uint16(buf)
	if _, err := ParseUnversionedHeader(buf); err == nil {
		t.Fatal("accepted a missing zero mask")
	}
}

// TestFragmentWideZeroMask covers the 32-bit-word path.
func TestFragmentWideZeroMask(t *testing.T) {
	// skip 0, 40 values, zeroes, last: raw = 0x0100 | 0x0080 | (40<<9) = 0x5180
	buf := make([]byte, 0, 2+8)
	buf = append(buf, 0x80, 0x51)
	// 40 bits -> two 32-bit words
	buf = append(buf, 0x01, 0x00, 0x00, 0x00)
	buf = append(buf, 0x00, 0x00, 0x00, 0x00)
	h, err := ParseUnversionedHeader(buf)
	if err != nil {
		t.Fatal(err)
	}
	if h.ZeroBits != 40 || len(h.ZeroMask) != 40 || !h.ZeroMask[0] || h.ZeroMask[1] {
		t.Fatalf("header %+v", h)
	}
}

// TestFragmentZeroMaskWidths pins the mask width rule from CUE4Parse FUnversionedHeader: one byte up
// to eight bits, two bytes up to sixteen, then 32-bit words. The boundaries are tested on both sides
// because a mask that is one byte too wide shifts every value in the row.
func TestFragmentZeroMaskWidths(t *testing.T) {
	cases := []struct{ values, wantBytes int }{
		{8, 1}, {9, 2}, {16, 2}, {17, 4}, {32, 4}, {33, 8},
	}
	for _, c := range cases {
		raw := uint16(c.values<<9) | 0x0080 | 0x0100
		buf := make([]byte, 2+c.wantBytes)
		binary.LittleEndian.PutUint16(buf, raw)
		h, err := ParseUnversionedHeader(buf)
		if err != nil {
			t.Fatalf("%d values: %v", c.values, err)
		}
		if h.ZeroBits != c.values {
			t.Fatalf("%d values: zero bits %d", c.values, h.ZeroBits)
		}
		if h.Size != 2+c.wantBytes {
			t.Fatalf("%d values: header size %d, want %d", c.values, h.Size, 2+c.wantBytes)
		}
		// one byte short must be refused, not silently read from the payload
		if _, err := ParseUnversionedHeader(buf[:len(buf)-1]); err == nil {
			t.Fatalf("%d values: accepted a header one byte short", c.values)
		}
	}
}
