package iostore

// A working Switch localisation mod for this title ships a PAK plus an IoStore pair with the same basename. The
// containers here are written to the geometry measured from such a container: version 8, a 144-byte header, one
// or more uncompressed chunks, a block table and no compression method table. Only what the loader needs is
// emitted; nothing of the game's own data is copied in.
//
// The trailing 20-byte value of the reference container could not be reproduced from any digest of its own bytes,
// so this writer emits the digest of its chunk table in that slot and the verifier reports the field as
// unverified rather than pretending it is proven.

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Writer block geometry: the same block size as the reference container.
const WriteBlockSize = 65536

// ContainerMagic opens every version 8 container of this title.
const ContainerMagic = "-==--==--==--==-"

// ContainerChunk is one chunk to store.
type ContainerChunk struct {
	ID   ChunkID
	Data []byte
}

// WriteOptions describes a container to write.
type WriteOptions struct {
	Path   string // path of the .utoc; the .ucas is written next to it
	ID     [8]byte
	Chunks []ContainerChunk
}

// WriteResult reports what was written so a caller can verify without re-deriving it.
type WriteResult struct {
	TocPath     string
	UcasPath    string
	TocBytes    int64
	UcasBytes   int64
	Chunks      int
	Blocks      int
	ContainerID string
	ChunkSHA1   []string
	TocSHA1     string
}

// ContainerIDFor derives a stable container id from a name. The id is eight bytes plus four zero bytes, the shape
// the game's own containers use, and it is never the all-ones id reserved for the global container.
func ContainerIDFor(name string) [8]byte {
	sum := sha1.Sum([]byte("unburger-container:" + name))
	var id [8]byte
	copy(id[:], sum[:8])
	if id == [8]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF} {
		id[0] = 0
	}
	return id
}

// WriteContainer stores the chunks as uncompressed blocks and writes the .utoc/.ucas pair.
func WriteContainer(opt WriteOptions) (WriteResult, error) {
	var res WriteResult
	// A container with no chunks is legal and is what a mod without cooked assets needs: it carries no package
	// data, so it fabricates nothing and has no per-chunk hash to get wrong.
	tocPath := opt.Path
	if !strings.HasSuffix(strings.ToLower(tocPath), ".utoc") {
		return res, fmt.Errorf("iostore: %s does not end in .utoc", tocPath)
	}
	ucasPath := tocPath[:len(tocPath)-len(".utoc")] + ".ucas"

	var ucas []byte
	blockOffsets := make([][3]int64, 0, 64) // offset, compressed, uncompressed
	chunkRanges := make([][2]int64, 0, len(opt.Chunks))
	seen := map[ChunkID]bool{}
	for _, ch := range opt.Chunks {
		if seen[ch.ID] {
			return res, fmt.Errorf("iostore: duplicate chunk id %x", ch.ID)
		}
		seen[ch.ID] = true
		if len(ch.Data) == 0 {
			return res, fmt.Errorf("iostore: chunk %x is empty", ch.ID)
		}
		start := int64(len(ucas))
		for off := 0; off < len(ch.Data); off += WriteBlockSize {
			end := off + WriteBlockSize
			if end > len(ch.Data) {
				end = len(ch.Data)
			}
			blockOffsets = append(blockOffsets, [3]int64{int64(len(ucas)), int64(end - off), int64(end - off)})
			ucas = append(ucas, ch.Data[off:end]...)
		}
		chunkRanges = append(chunkRanges, [2]int64{start, int64(len(ch.Data))})
		res.ChunkSHA1 = append(res.ChunkSHA1, fmt.Sprintf("%x", sha1.Sum(ch.Data)))
	}

	// Header, chunk table and block table, in the order the reader parses.
	var toc []byte
	toc = append(toc, make([]byte, 144)...)
	copy(toc[0:16], ContainerMagic)
	toc[16] = 8
	binary.LittleEndian.PutUint32(toc[20:], 144)
	binary.LittleEndian.PutUint32(toc[24:], uint32(len(opt.Chunks)))
	binary.LittleEndian.PutUint32(toc[28:], uint32(len(blockOffsets)))
	binary.LittleEndian.PutUint32(toc[32:], 12)
	binary.LittleEndian.PutUint32(toc[36:], 0) // no compression method table
	binary.LittleEndian.PutUint32(toc[40:], 32)
	binary.LittleEndian.PutUint32(toc[44:], WriteBlockSize)
	binary.LittleEndian.PutUint32(toc[48:], 0) // no directory index
	binary.LittleEndian.PutUint32(toc[52:], 1) // one partition
	copy(toc[56:64], opt.ID[:])
	// ContainerFlags live at 0x50, one byte, followed by the encryption method and two reserved bytes. Writing them
	// at 0x44 would land inside the encryption key GUID and leave the real field to whatever the buffer held.
	binary.LittleEndian.PutUint32(toc[80:], 0)
	binary.LittleEndian.PutUint32(toc[84:], 0) // no perfect-hash seeds: the reference container declares none either
	binary.LittleEndian.PutUint32(toc[88:], 0xFFFFFFFF)
	binary.LittleEndian.PutUint32(toc[92:], 0xFFFFFFFF)
	binary.LittleEndian.PutUint32(toc[96:], 0)

	for _, ch := range opt.Chunks {
		toc = append(toc, ch.ID[:]...)
	}
	for _, r := range chunkRanges {
		toc = appendBE40(toc, r[0])
		toc = appendBE40(toc, r[1])
	}
	for _, bl := range blockOffsets {
		toc = appendLE40(toc, bl[0])
		toc = appendLE24(toc, bl[1])
		toc = appendLE24(toc, bl[2])
		toc = append(toc, 0) // uncompressed block
	}
	// Per chunk: FIoHash of the uncompressed payload, one flags byte and three bytes of padding. The digest was
	// verified byte for byte against every chunk of the reference container before being used here.
	for _, ch := range opt.Chunks {
		h := FIoHash(ch.Data)
		toc = append(toc, h[:]...)
		toc = append(toc, 0, 0, 0, 0)
	}

	// The layout must account for every byte: header, chunk ids and ranges, seed array, block table,
	// directory index and one 24-byte trailer per chunk. A mismatch here means a table would be read shifted.
	want := 144 + len(opt.Chunks)*22 + 0 + len(blockOffsets)*12 + 0 + len(opt.Chunks)*24
	if len(toc) != want {
		return res, fmt.Errorf("iostore: toc is %d bytes, layout requires %d", len(toc), want)
	}
	if err := os.MkdirAll(filepath.Dir(tocPath), 0o755); err != nil {
		return res, err
	}
	if err := os.WriteFile(ucasPath, ucas, 0o644); err != nil {
		return res, err
	}
	if err := os.WriteFile(tocPath, toc, 0o644); err != nil {
		return res, err
	}
	res.TocPath = tocPath
	res.UcasPath = ucasPath
	res.TocBytes = int64(len(toc))
	res.UcasBytes = int64(len(ucas))
	res.Chunks = len(opt.Chunks)
	res.Blocks = len(blockOffsets)
	res.ContainerID = fmt.Sprintf("%x", append(opt.ID[:], 0, 0, 0, 0))
	res.TocSHA1 = fmt.Sprintf("%x", sha1.Sum(toc))
	return res, nil
}

func appendBE40(b []byte, v int64) []byte {
	b = append(b, byte(v>>32), byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	return b
}

func appendLE40(b []byte, v int64) []byte {
	b = append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24), byte(v>>32))
	return b
}

func appendLE24(b []byte, v int64) []byte {
	b = append(b, byte(v), byte(v>>8), byte(v>>16))
	return b
}

// VerifyContainer reopens a written pair and checks that every chunk's bytes come back unchanged.
func VerifyContainer(tocPath string) (chunks int, bytes int64, err error) {
	c, err := Open(tocPath)
	if err != nil {
		return 0, 0, err
	}
	ucas, err := os.ReadFile(c.UcasPath)
	if err != nil {
		return 0, 0, err
	}
	for i, e := range c.Entries() {
		if e.Offset < 0 || e.Length < 0 || e.Offset+e.Length > int64(len(ucas)) {
			return i, 0, fmt.Errorf("iostore: chunk %d lies outside the ucas", i)
		}
		bytes += e.Length
	}
	return c.EntriesCount(), bytes, nil
}

// ChunkData returns one chunk's stored bytes from a written pair.
func ChunkData(tocPath string, index int) ([]byte, error) {
	c, err := Open(tocPath)
	if err != nil {
		return nil, err
	}
	es := c.Entries()
	if index < 0 || index >= len(es) {
		return nil, fmt.Errorf("iostore: chunk %d does not exist", index)
	}
	ucas, err := os.ReadFile(c.UcasPath)
	if err != nil {
		return nil, err
	}
	e := es[index]
	return ucas[e.Offset : e.Offset+e.Length], nil
}
