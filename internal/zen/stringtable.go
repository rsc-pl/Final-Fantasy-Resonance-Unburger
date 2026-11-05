package zen

import (
	"encoding/binary"
	"fmt"
)

// StringTable is the serialized FStringTable export of an ST_ package.
type StringTable struct {
	Namespace string
	Keys      []string
	Values    []string
	Trailer   uint32
}

// ParseStringTable decodes the export payload of a StringTable package:
//
//	u16   version (1 for this build)
//	u32   table flags (0)
//	FString namespace
//	u32   entry count
//	per entry: FString key, FString value
//
// Strings use the same negative-length encoding as mapped names, so Japanese values are
// UTF-16 while keys stay ASCII.
func ParseStringTable(b []byte) (*StringTable, error) {
	if len(b) < 10 {
		return nil, fmt.Errorf("string table: %d bytes", len(b))
	}
	le := binary.LittleEndian
	pos := 0
	version := binary.BigEndian.Uint16(b[pos:])
	pos += 2
	if version != 1 {
		return nil, fmt.Errorf("string table: unsupported version %d", version)
	}
	flags := le.Uint32(b[pos:])
	pos += 4
	if flags != 0 {
		return nil, fmt.Errorf("string table: unsupported flags %#x", flags)
	}
	ns, err := readFStringPlain(b, &pos)
	if err != nil {
		return nil, fmt.Errorf("string table: namespace: %w", err)
	}
	if pos+4 > len(b) {
		return nil, fmt.Errorf("string table: missing entry count")
	}
	count := int(le.Uint32(b[pos:]))
	pos += 4
	if count < 0 || count > 1<<20 {
		return nil, fmt.Errorf("string table: implausible entry count %d", count)
	}
	st := &StringTable{Namespace: ns, Keys: make([]string, 0, count), Values: make([]string, 0, count)}
	for i := 0; i < count; i++ {
		key, err := readFStringPlain(b, &pos)
		if err != nil {
			return nil, fmt.Errorf("string table: key %d: %w", i, err)
		}
		value, err := readFStringPlain(b, &pos)
		if err != nil {
			return nil, fmt.Errorf("string table: value %d: %w", i, err)
		}
		st.Keys = append(st.Keys, key)
		st.Values = append(st.Values, value)
	}
	// The export ends with a 4-byte trailer in this build; anything else means the parse
	// drifted and the table is rejected.
	if rest := len(b) - pos; rest != 0 && rest != 4 {
		return nil, fmt.Errorf("string table: %d trailing bytes", rest)
	}
	if len(b)-pos == 4 {
		st.Trailer = binary.LittleEndian.Uint32(b[pos:])
	}
	return st, nil
}

// readFStringPlain reads an FString: int32 length, then bytes, negative length meaning UTF-16
// with -length characters.
func readFStringPlain(b []byte, pos *int) (string, error) {
	if *pos+4 > len(b) {
		return "", fmt.Errorf("truncated string length")
	}
	n := int(int32(binary.LittleEndian.Uint32(b[*pos:])))
	*pos += 4
	if n == 0 {
		return "", nil
	}
	if n > 0 {
		if *pos+n > len(b) {
			return "", fmt.Errorf("string of %d bytes outside the payload", n)
		}
		s := string(b[*pos : *pos+n-1])
		*pos += n
		return s, nil
	}
	units := -n
	if *pos+units*2 > len(b) {
		return "", fmt.Errorf("wide string of %d units outside the payload", units)
	}
	raw := b[*pos : *pos+units*2]
	*pos += units * 2
	runes := make([]rune, 0, units)
	for i := 0; i+1 < len(raw); i += 2 {
		r := rune(binary.LittleEndian.Uint16(raw[i:]))
		if r == 0 {
			break
		}
		runes = append(runes, r)
	}
	return string(runes), nil
}
