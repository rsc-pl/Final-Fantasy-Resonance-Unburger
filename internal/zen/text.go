package zen

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

// TextHistoryStringTable is ETextHistoryType::StringTableEntry, the only history this game uses
// for dialogue: the text lives in a string table asset under a key.
const TextHistoryStringTable = 11

// TextHistoryBase is ETextHistoryType::Base: a namespace, a key and the source string, all inline.
const TextHistoryBase = 0

// FText is a cooked FText value: flags, a history type and the history payload. String-table
// entries carry a table id (FName) and the key (FString). This shape is self-describing, so it can
// be decoded without the owning struct's schema.
type FText struct {
	Flags        uint32
	HistoryType  int8
	TableIDIndex uint32
	TableIDNum   uint32
	// Namespace and SourceString are set for a Base history; Key carries the key in both shapes.
	Namespace    string
	SourceString string
	Key          string
	Size         int
}

// ReadFText decodes one FText value, refusing unknown histories rather than guessing a width.
func ReadFText(b []byte, pos *int) (*FText, error) {
	start := *pos
	if start+5 > len(b) {
		return nil, fmt.Errorf("ftext: truncated header")
	}
	t := &FText{
		Flags:       binary.LittleEndian.Uint32(b[start:]),
		HistoryType: int8(b[start+4]),
	}
	p := start + 5
	if t.HistoryType == -1 {
		// ETextHistoryType::None — a cooked empty FText is flags plus the selector and nothing else.
		// This shape appears in real rows (an empty ContentText or an unset label), so it is read
		// rather than refused; every other unknown history still fails.
		t.Size = 5
		*pos = p
		return t, nil
	}
	if t.HistoryType != TextHistoryStringTable && t.HistoryType != TextHistoryBase {
		return nil, fmt.Errorf("ftext: unsupported history %d at %d", t.HistoryType, start)
	}
	if t.HistoryType == TextHistoryBase {
		// FTextHistory_Base carries a namespace, a key and the source string, all as FStrings.
		var err error
		if t.Namespace, err = readFStringAt(b, &p); err != nil {
			return nil, fmt.Errorf("ftext: base namespace: %w", err)
		}
		if t.Key, err = readFStringAt(b, &p); err != nil {
			return nil, fmt.Errorf("ftext: base key: %w", err)
		}
		if t.SourceString, err = readFStringAt(b, &p); err != nil {
			return nil, fmt.Errorf("ftext: base source: %w", err)
		}
		t.Size = p - start
		*pos = p
		return t, nil
	}
	if p+8 > len(b) {
		return nil, fmt.Errorf("ftext: truncated table id")
	}
	t.TableIDIndex = binary.LittleEndian.Uint32(b[p:]) & 0x3fffffff
	t.TableIDNum = binary.LittleEndian.Uint32(b[p+4:])
	p += 8
	key, n, err := readFString(b, p)
	if err != nil {
		return nil, err
	}
	t.Key = key
	p += n
	t.Size = p - start
	*pos = p
	return t, nil
}

// readFString reads an FString: a signed length and that many bytes; negative length means UTF-16.
func readFString(b []byte, pos int) (string, int, error) {
	if pos+4 > len(b) {
		return "", 0, fmt.Errorf("fstring: truncated length")
	}
	n := int(int32(binary.LittleEndian.Uint32(b[pos:])))
	off := pos + 4
	if n == 0 {
		return "", 4, nil
	}
	if n > 0 {
		if off+n > len(b) {
			return "", 0, fmt.Errorf("fstring: truncated body")
		}
		s := strings.TrimSuffix(string(b[off:off+n]), "\x00")
		return s, 4 + n, nil
	}
	units := -n
	if off+units*2 > len(b) {
		return "", 0, fmt.Errorf("fstring: truncated utf16 body")
	}
	u := make([]uint16, units)
	for i := 0; i < units; i++ {
		u[i] = binary.LittleEndian.Uint16(b[off+i*2:])
	}
	s := strings.TrimSuffix(string(utf16.Decode(u)), "\x00")
	return s, 4 + units*2, nil
}

// readFStringAt reads an FString at *pos and advances it.
func readFStringAt(b []byte, pos *int) (string, error) {
	s, n, err := readFString(b, *pos)
	if err != nil {
		return "", err
	}
	*pos += n
	return s, nil
}
