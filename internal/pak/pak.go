// Package pak reads Unreal Engine 4/5 .pak archives as found in cooked builds.
//
// The layout implemented here was recovered from a shipping Switch build and verified
// byte for byte (entry 0 is a 77-byte INI payload at 0x35; the first compressed entry
// decodes at 0xD9):
//
//	entry, 16-byte aligned:
//	  int64  offset          (absolute offset of this header; 0 for the first entry)
//	  int64  size            (stored bytes)
//	  int64  uncompressed    (decoded bytes)
//	  uint32 method          (0 = stored, otherwise an index into the pak method list)
//	  uint8  sha1[20]
//	  uint32 blockCount
//	  (uint64 start, uint64 end) * blockCount      -- relative to this header
//	  uint8  flags
//	  uint32 blockSize                              -- only when blockCount > 0
//	  data[size]
//
// and the full directory index at the offset/size stored in the index blob:
//
//	int32 dirCount
//	per directory: FString name, int32 fileCount, (FString file, uint32 location)*
//
// There is no subdirectory trailer; the root shows up as a literal "/" directory.
package pak

import (
	"encoding/binary"
	"fmt"
	"os"
)

// Entry is one file in the archive.
type Entry struct {
	Index        int
	Pos          int64
	Size         int64
	Uncompressed int64
	Method       uint32
	BlockCount   int
	Blocks       [][2]uint64
	Flags        uint8
	BlockSize    uint32
	DataOffset   int64
}

// HeaderLen is the fixed part of an entry header.
const HeaderLen = 53

// Reader is an opened archive.
type Reader struct {
	path    string
	f       *os.File
	size    int64
	entries []Entry
}

// Decoder expands one compressed block into exactly rawSize bytes.
type Decoder func(comp []byte, rawSize int) ([]byte, error)

// Open walks the archive. It stops at the first entry whose header does not validate,
// which is reported through Entries() length; callers that need every entry must treat a
// short walk as an error when the expected count is known.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	r := &Reader{path: path, f: f, size: st.Size()}
	pos := int64(0)
	var head [HeaderLen]byte
	for pos+HeaderLen <= r.size {
		if _, err := f.ReadAt(head[:], pos); err != nil {
			break
		}
		e, ok := parseEntry(head[:], pos)
		if !ok {
			break
		}
		if e.DataOffset+e.Size > r.size {
			break
		}
		e.Index = len(r.entries)
		r.entries = append(r.entries, e)
		next := e.DataOffset + e.Size
		pos = (next + 15) &^ 15
	}
	return r, nil
}

func parseEntry(b []byte, pos int64) (Entry, bool) {
	off := int64(binary.LittleEndian.Uint64(b[0:]))
	size := int64(binary.LittleEndian.Uint64(b[8:]))
	uncomp := int64(binary.LittleEndian.Uint64(b[16:]))
	method := binary.LittleEndian.Uint32(b[24:])
	if size < 0 || uncomp < 0 || size > 1<<31 || uncomp > 1<<31 {
		return Entry{}, false
	}
	if off != 0 && off != pos {
		return Entry{}, false
	}
	blocks := int(binary.LittleEndian.Uint32(b[48:]))
	if blocks < 0 || blocks > 4096 {
		return Entry{}, false
	}
	e := Entry{Pos: pos, Size: size, Uncompressed: uncomp, Method: method}
	e.BlockCount = blocks
	// Data starts after the fixed part, the block table, the flag byte and (for
	// compressed entries) the block size. Computing it here keeps the walk linear.
	header := int64(52 + 16*blocks + 1)
	if blocks > 0 {
		header += 4
	}
	e.DataOffset = pos + header
	return e, true
}

// Entries returns the walked entries.
func (r *Reader) Entries() []Entry { return r.entries }

// Size returns the archive size in bytes.
func (r *Reader) Size() int64 { return r.size }

// Path returns the archive path.
func (r *Reader) Path() string { return r.path }

// Close releases the file handle.
func (r *Reader) Close() error { return r.f.Close() }

// header reads the variable part of an entry header (block table, flags, block size).
func (r *Reader) header(e Entry) (Entry, error) {
	n := int64(HeaderLen) + int64(16*e.BlockCount)
	buf := make([]byte, n+5)
	if _, err := r.f.ReadAt(buf, e.Pos); err != nil {
		return e, err
	}
	p := int64(52)
	for i := 0; i < e.BlockCount; i++ {
		start := binary.LittleEndian.Uint64(buf[p:])
		end := binary.LittleEndian.Uint64(buf[p+8:])
		if end < start || start < uint64(HeaderLen) || int64(end)+e.Pos > r.size {
			return e, fmt.Errorf("pak: entry %d has an implausible block %d..%d", e.Index, start, end)
		}
		e.Blocks = append(e.Blocks, [2]uint64{start, end})
		p += 16
	}
	e.Flags = buf[p]
	p++
	if e.BlockCount > 0 {
		e.BlockSize = binary.LittleEndian.Uint32(buf[p:])
		p += 4
	}
	e.DataOffset = e.Pos + p
	return e, nil
}

// Read returns the decoded payload of an entry. Stored entries need no decoder.
func (r *Reader) Read(e Entry, decode Decoder) ([]byte, error) {
	e, err := r.header(e)
	if err != nil {
		return nil, err
	}
	if e.BlockCount == 0 {
		out := make([]byte, e.Size)
		if _, err := r.f.ReadAt(out, e.DataOffset); err != nil {
			return nil, err
		}
		return out, nil
	}
	if decode == nil {
		return nil, fmt.Errorf("pak: entry %d is compressed and no decoder is configured", e.Index)
	}
	out := make([]byte, 0, e.Uncompressed)
	for i, blk := range e.Blocks {
		raw := e.Uncompressed - int64(len(out))
		if e.BlockSize > 0 && int64(e.BlockSize) < raw {
			raw = int64(e.BlockSize)
		}
		comp := make([]byte, blk[1]-blk[0])
		if _, err := r.f.ReadAt(comp, e.Pos+int64(blk[0])); err != nil {
			return nil, err
		}
		chunk, err := decode(comp, int(raw))
		if err != nil {
			return nil, fmt.Errorf("pak: block %d of entry %d: %w", i, e.Index, err)
		}
		out = append(out, chunk...)
	}
	if int64(len(out)) != e.Uncompressed {
		return nil, fmt.Errorf("pak: entry %d decoded %d of %d bytes", e.Index, len(out), e.Uncompressed)
	}
	return out, nil
}
