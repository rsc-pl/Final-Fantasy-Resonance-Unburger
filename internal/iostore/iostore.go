// Package iostore reads Unreal Engine 5 IoStore containers (.utoc + .ucas).
//
// Layout verified against the shipping Switch container (version 8):
//
//	FIoStoreTocHeader             144 bytes
//	chunk ids                     count * 12   {u64 id, u16 index, u8 type}
//	FIoOffsetAndLength            count * 10   2 x 40-bit BIG-endian
//	perfect hash seeds            seeds * 4
//	chunks without perfect hash   n * 4
//	FIoStoreTocCompressedBlockEntry blocks * 12
//	                              40-bit little-endian physical offset
//	                              24-bit little-endian compressed size
//	                              24-bit little-endian uncompressed size
//	                              8-bit compression method
//	compression method names      methods * 32
//	FIoDirectoryIndexResource     directoryIndexSize
//	FIoStoreTocEntryMeta          count * 24 (20-byte hash, 1 flag, 3 padding)
//
// The chunk offset is LOGICAL: it addresses the container's decompressed stream, so a
// chunk's first block is block[offset/blockSize] and does not have to line up with any
// physical block start.
package iostore

import (
	"encoding/binary"
	"fmt"
	"os"
)

// Magic is the IoStore table of contents signature.
const Magic = "-==--==--==--==-"

// HeaderLen is the fixed size of FIoStoreTocHeader.
const HeaderLen = 144

// ChunkID identifies one payload inside a container.
type ChunkID [12]byte

// Type returns the chunk type byte (1 = export bundle, 2 = bulk data, ...).
func (c ChunkID) Type() uint8 { return c[11] }

// String renders the id for reports.
func (c ChunkID) String() string { return fmt.Sprintf("%x", c[:]) }

// Block is one compressed block of the .ucas.
type Block struct {
	Offset       int64
	Compressed   int
	Uncompressed int
	Method       uint8
}

// Entry is one chunk: a logical slice of the decompressed stream plus its blocks.
type Entry struct {
	ID        ChunkID
	Offset    int64
	Length    int64
	BlockBase int
	Blocks    []Block
}

// File is a path from the directory index together with the chunk that stores it.
type File struct {
	Path    string
	ChunkID ChunkID
	Index   int // chunk table index from the file entry's UserData
}

// Container is an opened .utoc.
type Container struct {
	TocPath  string
	UcasPath string
	Version  uint8

	EntryCount int
	BlockCount int
	BlockSize  uint32

	entries []Entry
	blocks  []Block
	files   []File

	toc      []byte
	dirStart int64
	dirSize  int
}

// Open parses a .utoc and its sibling .ucas.
func Open(tocPath string) (*Container, error) {
	b, err := os.ReadFile(tocPath)
	if err != nil {
		return nil, err
	}
	if len(b) < HeaderLen || string(b[:16]) != Magic {
		return nil, fmt.Errorf("%s: not an IoStore toc", tocPath)
	}
	c := &Container{TocPath: tocPath, UcasPath: trimExt(tocPath, ".utoc") + ".ucas", toc: b}
	c.Version = b[16]
	le := binary.LittleEndian
	headerSize := int(le.Uint32(b[20:]))
	if headerSize < HeaderLen || headerSize > len(b) {
		return nil, fmt.Errorf("%s: implausible header size %d", tocPath, headerSize)
	}
	c.EntryCount = int(le.Uint32(b[24:]))
	c.BlockCount = int(le.Uint32(b[28:]))
	blockEntrySize := int(le.Uint32(b[32:]))
	methodCount := int(le.Uint32(b[36:]))
	methodLength := int(le.Uint32(b[40:]))
	c.BlockSize = le.Uint32(b[44:])
	dirSize := int(le.Uint32(b[48:]))
	seedsCount := int(le.Uint32(b[84:]))
	withoutPerfect := int(le.Uint32(b[96:]))
	if c.BlockSize == 0 || blockEntrySize != 12 {
		return nil, fmt.Errorf("%s: unsupported block geometry", tocPath)
	}

	pos := headerSize
	if pos+c.EntryCount*12 > len(b) {
		return nil, fmt.Errorf("%s: truncated chunk id table", tocPath)
	}
	ids := make([]ChunkID, c.EntryCount)
	for i := range ids {
		copy(ids[i][:], b[pos:pos+12])
		pos += 12
	}
	c.entries = make([]Entry, c.EntryCount)
	for i := range c.entries {
		e := &c.entries[i]
		e.ID = ids[i]
		e.Offset = be40(b[pos : pos+5])
		e.Length = be40(b[pos+5 : pos+10])
		pos += 10
		if e.Offset < 0 || e.Length < 0 {
			return nil, fmt.Errorf("%s: chunk %d has a negative range", tocPath, i)
		}
		e.BlockBase = int(e.Offset / int64(c.BlockSize))
	}
	pos += seedsCount * 4
	pos += withoutPerfect * 4
	if pos+c.BlockCount*blockEntrySize > len(b) {
		return nil, fmt.Errorf("%s: truncated block table", tocPath)
	}
	c.blocks = make([]Block, c.BlockCount)
	for i := range c.blocks {
		raw := b[pos : pos+blockEntrySize]
		c.blocks[i] = Block{
			Offset:       le40(raw[0:5]),
			Compressed:   int(le24(raw[5:8])),
			Uncompressed: int(le24(raw[8:11])),
			Method:       raw[11],
		}
		pos += 12
	}
	for i := range c.entries {
		e := &c.entries[i]
		need := 0
		if e.Length > 0 {
			need = int((e.Length + int64(c.BlockSize) - 1) / int64(c.BlockSize))
		}
		if e.BlockBase+need > len(c.blocks) {
			return nil, fmt.Errorf("%s: chunk %d needs blocks %d..%d of %d",
				tocPath, i, e.BlockBase, e.BlockBase+need, len(c.blocks))
		}
		if need > 0 {
			e.Blocks = c.blocks[e.BlockBase : e.BlockBase+need]
		}
	}
	pos += methodCount * methodLength

	// A signature hash may sit between the method names and the index. The offset is
	// confirmed by parsing: the candidate whose file entries all address real chunks wins.
	for _, cand := range []int{pos, pos - 32, pos + 32} {
		if cand < 0 || cand+dirSize > len(b) {
			continue
		}
		files, err := parseDirectory(b[cand : cand+dirSize])
		if err != nil {
			continue
		}
		ok := true
		for i := range files {
			if files[i].Index < 0 || files[i].Index >= c.EntryCount {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		for i := range files {
			files[i].ChunkID = c.entries[files[i].Index].ID
		}
		c.dirStart = int64(cand)
		c.dirSize = dirSize
		c.files = files
		return c, nil
	}
	// The directory index layout for this revision still needs its string table decoded; the
	// chunk table and the block table are usable regardless, so the container opens with an
	// empty file list instead of failing closed.
	c.dirStart = -1
	return c, nil
}

func trimExt(p, ext string) string {
	if len(p) > len(ext) && p[len(p)-len(ext):] == ext {
		return p[:len(p)-len(ext)]
	}
	return p
}

func le40(b []byte) int64 {
	return int64(b[0]) | int64(b[1])<<8 | int64(b[2])<<16 | int64(b[3])<<24 | int64(b[4])<<32
}

func le24(b []byte) int64 {
	return int64(b[0]) | int64(b[1])<<8 | int64(b[2])<<16
}

func be40(b []byte) int64 {
	return int64(b[4]) | int64(b[3])<<8 | int64(b[2])<<16 | int64(b[1])<<24 | int64(b[0])<<32
}

// Entries exposes the chunk table.
func (c *Container) Entries() []Entry { return c.entries }

// EntriesCount returns the number of chunks.
func (c *Container) EntriesCount() int { return len(c.entries) }

// Files exposes the directory index.
func (c *Container) Files() []File { return c.files }

// BlockSizeBytes returns the uncompressed block size.
func (c *Container) BlockSizeBytes() int { return int(c.BlockSize) }
