package locres

import (
	"bytes"
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

func writeString(b *bytes.Buffer, s string) {
	u := utf16.Encode([]rune(s))
	binary.Write(b, binary.LittleEndian, int32(-len(u)-1))
	for _, v := range u {
		binary.Write(b, binary.LittleEndian, v)
	}
	binary.Write(b, binary.LittleEndian, uint16(0))
}
func sample(version byte) []byte {
	b := new(bytes.Buffer)
	b.Write(magic[:])
	b.WriteByte(version)
	binary.Write(b, binary.LittleEndian, int64(0))
	if version >= 2 {
		binary.Write(b, binary.LittleEndian, int32(1))
	}
	binary.Write(b, binary.LittleEndian, int32(1))
	if version >= 2 {
		binary.Write(b, binary.LittleEndian, uint32(0))
	}
	writeString(b, "Dialog")
	binary.Write(b, binary.LittleEndian, int32(1))
	if version >= 2 {
		binary.Write(b, binary.LittleEndian, uint32(0))
	}
	writeString(b, "line_01")
	binary.Write(b, binary.LittleEndian, uint32(1234))
	binary.Write(b, binary.LittleEndian, int32(0))
	offset := int64(b.Len())
	binary.Write(b, binary.LittleEndian, int32(1))
	writeString(b, "こんにちは")
	if version >= 2 {
		binary.Write(b, binary.LittleEndian, int32(1))
	}
	data := b.Bytes()
	binary.LittleEndian.PutUint64(data[17:25], uint64(offset))
	return data
}
func TestParseVersions(t *testing.T) {
	for _, v := range []byte{1, 2, 3} {
		f, e := Parse(sample(v))
		if e != nil {
			t.Fatal(v, e)
		}
		if f.Version != v || len(f.Entries) != 1 || f.Entries[0].Text != "こんにちは" {
			t.Fatalf("wrong v%d: %#v", v, f)
		}
	}
}
func TestBadIndex(t *testing.T) {
	b := sample(3)
	b[57] = 0xff
	b[58] = 0xff
	b[59] = 0xff
	b[60] = 0xff
	if _, e := Parse(b); e == nil {
		t.Fatal("corrupt input accepted")
	}
}
func TestUnknownVersion(t *testing.T) {
	b := sample(3)
	b[16] = 4
	if _, e := Parse(b); e == nil {
		t.Fatal("unknown version accepted")
	}
}
