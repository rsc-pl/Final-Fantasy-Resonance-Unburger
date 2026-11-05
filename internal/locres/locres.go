package locres

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf16"
	"unicode/utf8"
)

var magic = [16]byte{0x0e, 0x14, 0x74, 0x75, 0x67, 0x4a, 0x03, 0xfc, 0x4a, 0x15, 0x90, 0x9d, 0xc3, 0x37, 0x7f, 0x1b}

const maxItems = 2000000
const maxString = 16 << 20

type Entry struct {
	Namespace  string `json:"namespace"`
	Key        string `json:"key"`
	Text       string `json:"text"`
	SourceHash uint32 `json:"source_hash"`
	// NamespaceHash and KeyHash are the identifiers the engine looks entries up by. They are carried, not
	// recomputed: the values written by the cooker are the only ones the running game accepts as equal.
	NamespaceHash uint32 `json:"namespace_hash"`
	KeyHash       uint32 `json:"key_hash"`
}
type File struct {
	Version uint8   `json:"version"`
	Entries []Entry `json:"entries"`
}
type reader struct {
	data []byte
	off  int64
}

func (r *reader) seek(i int64) error {
	if i < 0 || i > int64(len(r.data)) {
		return io.ErrUnexpectedEOF
	}
	r.off = i
	return nil
}
func (r *reader) take(n int64) ([]byte, error) {
	if n < 0 || n > int64(len(r.data))-r.off {
		return nil, io.ErrUnexpectedEOF
	}
	s := r.data[r.off : r.off+n]
	r.off += n
	return s, nil
}
func (r *reader) u8() (uint8, error) {
	b, e := r.take(1)
	if e != nil {
		return 0, e
	}
	return b[0], nil
}
func (r *reader) u32() (uint32, error) {
	b, e := r.take(4)
	if e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint32(b), nil
}
func (r *reader) i32() (int32, error) { x, e := r.u32(); return int32(x), e }
func (r *reader) i64() (int64, error) {
	b, e := r.take(8)
	if e != nil {
		return 0, e
	}
	return int64(binary.LittleEndian.Uint64(b)), nil
}
func (r *reader) count() (int, error) {
	n, e := r.i32()
	if e != nil {
		return 0, e
	}
	if n < 0 || n > maxItems {
		return 0, fmt.Errorf("invalid count %d", n)
	}
	return int(n), nil
}
func (r *reader) str() (string, error) {
	n, e := r.i32()
	if e != nil {
		return "", e
	}
	if n == 0 {
		return "", nil
	}
	if n > 0 {
		if n > maxString {
			return "", errors.New("oversize text")
		}
		b, e := r.take(int64(n))
		if e != nil {
			return "", e
		}
		if b[len(b)-1] != 0 || !utf8.Valid(b[:len(b)-1]) {
			return "", errors.New("invalid ANSI string")
		}
		return string(b[:len(b)-1]), nil
	}
	if n == -2147483648 || -n > maxString/2 {
		return "", errors.New("oversize UTF16 text")
	}
	b, e := r.take(int64(-n) * 2)
	if e != nil {
		return "", e
	}
	count := int(-n)
	u := make([]uint16, count)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	if u[count-1] != 0 {
		return "", errors.New("missing UTF16 terminator")
	}
	return string(utf16.Decode(u[:count-1])), nil
}
func Parse(data []byte) (File, error) {
	f := File{Entries: []Entry{}}
	r := reader{data: data}
	if len(data) >= 16 && bytes.Equal(data[:16], magic[:]) {
		r.off = 16
		version, e := r.u8()
		if e != nil {
			return f, e
		}
		if version < 1 || version > 3 {
			return f, fmt.Errorf("unsupported locres format %d", version)
		}
		f.Version = version
	} else {
		f.Version = 0
	}
	lookup := []string{}
	if f.Version >= 1 {
		offset, e := r.i64()
		if e != nil {
			return f, e
		}
		saved := r.off
		if offset < 0 {
			return f, errors.New("missing locres string pool")
		}
		if e = r.seek(offset); e != nil {
			return f, e
		}
		n, e := r.count()
		if e != nil {
			return f, e
		}
		for i := 0; i < n; i++ {
			s, e := r.str()
			if e != nil {
				return f, e
			}
			lookup = append(lookup, s)
			if f.Version >= 2 {
				if _, e = r.i32(); e != nil {
					return f, e
				}
			}
		}
		if e = r.seek(saved); e != nil {
			return f, e
		}
	}
	total := 0
	if f.Version >= 2 {
		n, e := r.count()
		if e != nil {
			return f, e
		}
		total = n
	}
	count, e := r.count()
	if e != nil {
		return f, e
	}
	for i := 0; i < count; i++ {
		var nsHash uint32
		if f.Version >= 2 {
			if nsHash, e = r.u32(); e != nil {
				return f, e
			}
		}
		ns, e := r.str()
		if e != nil {
			return f, e
		}
		keys, e := r.count()
		if e != nil {
			return f, e
		}
		for j := 0; j < keys; j++ {
			var keyHash uint32
			if f.Version >= 2 {
				if keyHash, e = r.u32(); e != nil {
					return f, e
				}
			}
			key, e := r.str()
			if e != nil {
				return f, e
			}
			srcHash, e := r.u32()
			if e != nil {
				return f, e
			}
			value := ""
			if f.Version >= 1 {
				idx, e := r.i32()
				if e != nil {
					return f, e
				}
				if idx < 0 || int(idx) >= len(lookup) {
					return f, fmt.Errorf("invalid pool index %d", idx)
				}
				value = lookup[idx]
			} else {
				value, e = r.str()
				if e != nil {
					return f, e
				}
			}
			f.Entries = append(f.Entries, Entry{Namespace: ns, Key: key, Text: value, SourceHash: srcHash,
				NamespaceHash: nsHash, KeyHash: keyHash})
			if len(f.Entries) > maxItems {
				return f, errors.New("too many entries")
			}
		}
	}
	if f.Version >= 2 && total != len(f.Entries) {
		return f, fmt.Errorf("entry count mismatch %d != %d", total, len(f.Entries))
	}
	return f, nil
}
func Read(path string) (File, error) {
	st, e := os.Stat(path)
	if e != nil {
		return File{}, e
	}
	if st.Size() > 256<<20 {
		return File{}, errors.New("locres too large")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return File{}, e
	}
	return Parse(b)
}
