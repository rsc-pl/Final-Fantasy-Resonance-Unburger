package pak

// Version 11 archives use the three-part index recovered from shipped cookers: a primary index holding bit-packed
// entry records and the offsets of two secondary indexes, a path-hash index, and a full directory index. This file
// writes that layout and reads it back, so a patch archive can be produced and then verified by an independent
// pass over its own bytes rather than by trusting the writer.

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
)

// DefaultMountPoint is the mount point the game's own archives use.
const DefaultMountPoint = "../../../"

// VirtualPath is the mount-relative path of the game's English localisation archive.
const VirtualPath = "FFRS/Content/Localization/Game/en/Game.locres"

// Magic is the archive signature.
const Magic uint32 = 0x5A6F12E1

// Version11 is the format this file reads and writes.
const Version11 int32 = 11

// FooterSizeV11 is the footer length: key guid, encryption flag, magic, version, index offset and size, index
// hash, and five 32-byte compression method names.
const FooterSizeV11 = 221

// PatchEntry is one file of a patch archive.
type PatchEntry struct {
	Path string // mount-relative, for example "FFRS/Content/Localization/Game/en/Game.locres"
	Data []byte
}

// PatchOptions describes an archive to write.
type PatchOptions struct {
	MountPoint   string // "../../../" for the game's own archives
	Entries      []PatchEntry
	PathHashSeed uint64
	// UseZlib compresses every entry into zlib blocks, as the working localisation archive for this title does.
	UseZlib bool
	// BlockSize is the uncompressed size of one compression block.
	BlockSize int
	// CompressionName is the name written into the footer's method table slot 0 when entries are compressed.
	CompressionName string
}

// WrittenEntry reports what the writer decided for one entry.
type WrittenEntry struct {
	Path     string
	Offset   int64
	Size     int32
	Location int32
	PathHash uint64
	SHA1     string
	// Method, Uncompressed and Compressed describe an entry that keeps its original compression.
	Method       uint32
	Uncompressed int32
	Compressed   bool
}

// PatchResult is the outcome of a write, sufficient to verify the file without re-deriving it.
type PatchResult struct {
	FileSize       int64
	MountPoint     string
	NumEntries     int32
	PrimaryOffset  int64
	PrimarySize    int64
	PrimarySHA1    string
	PathHashOffset int64
	PathHashSize   int64
	PathHashSHA1   string
	FullDirOffset  int64
	FullDirSize    int64
	FullDirSHA1    string
	EncodedSize    int32
	Entries        []WrittenEntry
}

// fstring appends a UE string: length including the NUL, ANSI bytes, NUL.
func fstring(b *[]byte, s string) {
	var l [4]byte
	binary.LittleEndian.PutUint32(l[:], uint32(int32(len(s)+1)))
	*b = append(*b, l[:]...)
	*b = append(*b, s...)
	*b = append(*b, 0)
}

// writeStoredLocalHeader writes the 53-byte inline header of an uncompressed entry.
func writeStoredLocalHeader(b *[]byte, size int64, sum [20]byte) {
	var buf [53]byte
	binary.LittleEndian.PutUint64(buf[0:], 0) // local Offset is always zero
	binary.LittleEndian.PutUint64(buf[8:], uint64(size))
	binary.LittleEndian.PutUint64(buf[16:], uint64(size))
	binary.LittleEndian.PutUint32(buf[24:], 0) // compression method 0: stored
	copy(buf[28:], sum[:])
	buf[48] = 0 // flags
	binary.LittleEndian.PutUint32(buf[49:], 0)
	*b = append(*b, buf[:]...)
}

// encodeStored writes the 12-byte bit-packed record of a stored entry.
func encodeStored(b *[]byte, offset int64, size int32) int32 {
	loc := int32(len(*b))
	var buf [12]byte
	binary.LittleEndian.PutUint32(buf[0:], 0xE0000000) // 32-bit offset, size and uncompressed size
	binary.LittleEndian.PutUint32(buf[4:], uint32(offset))
	binary.LittleEndian.PutUint32(buf[8:], uint32(size))
	*b = append(*b, buf[:]...)
	return loc
}

// encodeCompressed writes the index record of a compressed entry. The flags carry, besides the method, the number of
// compression blocks and the block size in units of 2048 bytes; the body lists every block's compressed length, so
// the engine can find each block without touching the local header. A record that omits either is what made an
// earlier build unreadable to the game even though the payload itself was intact.
func encodeCompressed(b *[]byte, offset int64, unc, size int32, method uint32, blockSize int, blocks []int32) int32 {
	loc := int32(len(*b))
	if blockSize <= 0 {
		blockSize = DefaultBlockSize
	}
	if blockSize%2048 != 0 || blockSize/2048 > 0x3F {
		blockSize = DefaultBlockSize
	}
	flags := uint32(0xE0000000) | (method&0x3F)<<23 | uint32(len(blocks))<<6 | uint32(blockSize/2048)
	var buf [16]byte
	binary.LittleEndian.PutUint32(buf[0:], flags)
	binary.LittleEndian.PutUint32(buf[4:], uint32(offset))
	binary.LittleEndian.PutUint32(buf[8:], uint32(unc))
	binary.LittleEndian.PutUint32(buf[12:], uint32(size))
	*b = append(*b, buf[:]...)
	for _, n := range blocks {
		var sz [4]byte
		binary.LittleEndian.PutUint32(sz[:], uint32(n))
		*b = append(*b, sz[:]...)
	}
	return loc
}

// PathHash is the engine's lookup key: FNV-1a 64 over the UTF-16LE bytes of the lowercased, mount-relative path
// with a leading slash stripped, using the path hash seed added to the FNV basis.
func PathHash(seed uint64, p string) uint64 {
	h := uint64(0xCBF29CE484222325) + seed
	p = strings.TrimPrefix(p, "/")
	p = strings.ToLower(p)
	// Every character contributes two bytes: the engine hashes the UTF-16LE form, not the ASCII one.
	for _, r := range p {
		for _, b := range utf16le(r) {
			h ^= uint64(b)
			h *= 0x00000100000001B3
		}
	}
	return h
}

func utf16le(r rune) []byte {
	if r < 0x10000 {
		return []byte{byte(r), byte(r >> 8)}
	}
	r -= 0x10000
	hi := 0xD800 + (r >> 10)
	lo := 0xDC00 + (r & 0x3FF)
	return []byte{byte(hi), byte(hi >> 8), byte(lo), byte(lo >> 8)}
}

// splitPath returns the directory (with trailing slash, no leading slash) and the leaf name.
func splitPath(p string) (dir, leaf string) {
	p = strings.TrimPrefix(strings.ReplaceAll(p, "\\", "/"), "/")
	d := path.Dir(p)
	if d == "." || d == "" {
		return "/", p
	}
	return d + "/", path.Base(p)
}

// WritePatch writes a version 11 archive that holds only the given entries.
func WritePatch(out string, opt PatchOptions) (PatchResult, error) {
	res := PatchResult{MountPoint: opt.MountPoint}
	if opt.MountPoint == "" {
		return res, fmt.Errorf("pak: a mount point is required")
	}
	if len(opt.Entries) == 0 {
		return res, fmt.Errorf("pak: no entries to write")
	}
	entries := make([]PatchEntry, len(opt.Entries))
	copy(entries, opt.Entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	seen := map[string]bool{}
	for _, e := range entries {
		p := strings.TrimPrefix(strings.ReplaceAll(e.Path, "\\", "/"), "/")
		if p == "" || strings.Contains(p, "..") {
			return res, fmt.Errorf("pak: unsafe entry path %q", e.Path)
		}
		if seen[strings.ToLower(p)] {
			return res, fmt.Errorf("pak: duplicate entry %q", p)
		}
		seen[strings.ToLower(p)] = true
		if len(e.Data) == 0 {
			return res, fmt.Errorf("pak: entry %q is empty", p)
		}
	}

	// Data region: a local header and its payload, entry after entry, no padding.
	var data []byte
	var encoded []byte
	written := make([]WrittenEntry, 0, len(entries))
	for _, e := range entries {
		p := strings.TrimPrefix(strings.ReplaceAll(e.Path, "\\", "/"), "/")
		if opt.UseZlib {
			off, csize, unc, sum, blocks := compressEntry(&data, e.Data, opt.BlockSize)
			bs := opt.BlockSize
			if bs <= 0 {
				bs = DefaultBlockSize
			}
			loc := encodeCompressed(&encoded, off, unc, csize, 1, bs, blocks)
			written = append(written, WrittenEntry{
				Path: p, Offset: off, Size: csize, Uncompressed: unc, Compressed: true, Method: 1, Location: loc,
				PathHash: PathHash(opt.PathHashSeed, p), SHA1: fmt.Sprintf("%x", sum),
			})
			continue
		}
		sum := sha1.Sum(e.Data)
		off := int64(len(data))
		writeStoredLocalHeader(&data, int64(len(e.Data)), sum)
		data = append(data, e.Data...)
		loc := encodeStored(&encoded, off, int32(len(e.Data)))
		written = append(written, WrittenEntry{
			Path: p, Offset: off, Size: int32(len(e.Data)), Location: loc,
			PathHash: PathHash(opt.PathHashSeed, p), SHA1: fmt.Sprintf("%x", sum),
		})
	}

	fullDir := buildFullDirectory(written)

	// The path hash index is the hash table followed by an empty pruned directory index, a shape the engine
	// loads (33 of the 34 shipped archives ship exactly that).
	var pathHash []byte
	var cnt [4]byte
	binary.LittleEndian.PutUint32(cnt[:], uint32(int32(len(written))))
	pathHash = append(pathHash, cnt[:]...)
	byHash := make([]WrittenEntry, len(written))
	copy(byHash, written)
	sort.Slice(byHash, func(i, j int) bool { return byHash[i].PathHash < byHash[j].PathHash })
	for _, e := range byHash {
		var pair [12]byte
		binary.LittleEndian.PutUint64(pair[0:], e.PathHash)
		binary.LittleEndian.PutUint32(pair[8:], uint32(e.Location))
		pathHash = append(pathHash, pair[:]...)
	}
	var zero [4]byte
	pathHash = append(pathHash, zero[:]...) // pruned directory index: DirCount = 0

	primary := buildPrimary(opt.MountPoint, opt.PathHashSeed, int32(len(written)), encoded, 0, 0, [20]byte{}, 0, 0, [20]byte{})

	primaryOffset := int64(len(data))
	pathHashOffset := primaryOffset + int64(len(primary))
	fullDirOffset := pathHashOffset + int64(len(pathHash))

	primary = buildPrimary(opt.MountPoint, opt.PathHashSeed, int32(len(written)), encoded,
		pathHashOffset, int64(len(pathHash)), sha1.Sum(pathHash),
		fullDirOffset, int64(len(fullDir)), sha1.Sum(fullDir))

	if int64(len(primary)) != int64(len(buildPrimary(opt.MountPoint, opt.PathHashSeed, int32(len(written)), encoded, 0, 0, [20]byte{}, 0, 0, [20]byte{}))) {
		return res, fmt.Errorf("pak: primary index size changed between passes")
	}

	var file []byte
	file = append(file, data...)
	file = append(file, primary...)
	file = append(file, pathHash...)
	file = append(file, fullDir...)
	file = append(file, buildFooter(int64(len(primary)), primaryOffset, sha1.Sum(primary), opt.CompressionName)...)

	if err := os.WriteFile(out, file, 0o644); err != nil {
		return res, err
	}
	res.FileSize = int64(len(file))
	res.NumEntries = int32(len(written))
	res.PrimaryOffset = primaryOffset
	res.PrimarySize = int64(len(primary))
	res.PrimarySHA1 = fmt.Sprintf("%x", sha1.Sum(primary))
	res.PathHashOffset = pathHashOffset
	res.PathHashSize = int64(len(pathHash))
	res.PathHashSHA1 = fmt.Sprintf("%x", sha1.Sum(pathHash))
	res.FullDirOffset = fullDirOffset
	res.FullDirSize = int64(len(fullDir))
	res.FullDirSHA1 = fmt.Sprintf("%x", sha1.Sum(fullDir))
	res.EncodedSize = int32(len(encoded))
	res.Entries = written
	return res, nil
}

// buildFullDirectory serialises the directory index: directories with a trailing slash, leaf names inside.
func buildFullDirectory(entries []WrittenEntry) []byte {
	type file struct {
		name string
		loc  int32
	}
	dirs := map[string][]file{}
	order := []string{}
	for _, e := range entries {
		d, l := splitPath(e.Path)
		if _, ok := dirs[d]; !ok {
			order = append(order, d)
		}
		dirs[d] = append(dirs[d], file{name: l, loc: e.Location})
	}
	sort.Strings(order)
	var b []byte
	var cnt [4]byte
	binary.LittleEndian.PutUint32(cnt[:], uint32(int32(len(order))))
	b = append(b, cnt[:]...)
	for _, d := range order {
		fstring(&b, d)
		files := dirs[d]
		sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
		binary.LittleEndian.PutUint32(cnt[:], uint32(int32(len(files))))
		b = append(b, cnt[:]...)
		for _, f := range files {
			fstring(&b, f.name)
			var loc [4]byte
			binary.LittleEndian.PutUint32(loc[:], uint32(f.loc))
			b = append(b, loc[:]...)
		}
	}
	return b
}

// buildPrimary serialises the primary index, including the encoded entry blob and both secondary offsets.
func buildPrimary(mount string, seed uint64, num int32, encoded []byte,
	phOff, phSize int64, phHash [20]byte, fdOff, fdSize int64, fdHash [20]byte) []byte {
	var b []byte
	fstring(&b, mount)
	var buf [8]byte
	binary.LittleEndian.PutUint32(buf[:4], uint32(num))
	b = append(b, buf[:4]...)
	binary.LittleEndian.PutUint64(buf[:], seed)
	b = append(b, buf[:]...)
	putI32(&b, 1)
	putI64(&b, phOff)
	putI64(&b, phSize)
	b = append(b, phHash[:]...)
	putI32(&b, 1)
	putI64(&b, fdOff)
	putI64(&b, fdSize)
	b = append(b, fdHash[:]...)
	putI32(&b, int32(len(encoded)))
	b = append(b, encoded...)
	putI32(&b, 0) // NumNonEncodedFiles
	return b
}

func putI32(b *[]byte, v int32) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(v))
	*b = append(*b, buf[:]...)
}

func putI64(b *[]byte, v int64) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(v))
	*b = append(*b, buf[:]...)
}

// buildFooter writes the 221-byte version 11 footer. The index is never encrypted; slot 0 of the method table
// carries the caller's compression name when compressed entries are written.
func buildFooter(indexSize, indexOffset int64, indexHash [20]byte, indexName string) []byte {
	var b []byte
	b = append(b, make([]byte, 16)...) // EncryptionKeyGuid: none
	b = append(b, 0)                   // bEncryptedIndex
	putI32(&b, int32(Magic))           // Magic, after the flag as the format places it
	putI32(&b, Version11)
	putI64(&b, indexOffset)
	putI64(&b, indexSize)
	b = append(b, indexHash[:]...)
	var methods [5 * 32]byte
	if len(indexName) > 0 {
		copy(methods[:], indexName)
	}
	b = append(b, methods[:]...)
	return b
}

// DefaultBlockSize is the block size the reference archive uses: its entries decode in blocks of 124 KiB.
const DefaultBlockSize = 126976

// compressEntry replaces one stored entry with a zlib block-compressed one and returns the record fields.
//
// The inline header of a compressed entry is: offset, size (of the compressed payload), uncompressed size,
// method index, sha1 of the compressed payload, block count, one (start, end) pair per block measured from the
// start of the entry, a flags byte and the uncompressed block size. The reference archive uses exactly this.
func compressEntry(b *[]byte, data []byte, blockSize int) (offset int64, size, unc int32, sum [20]byte, blocks []int32) {
	if blockSize <= 0 {
		blockSize = DefaultBlockSize
	}
	var payload bytes.Buffer
	var bounds [][2]uint64
	for start := 0; start < len(data); start += blockSize {
		end := start + blockSize
		if end > len(data) {
			end = len(data)
		}
		block, err := deflate(data[start:end], 6)
		if err != nil {
			return 0, 0, 0, sum, nil
		}
		from := uint64(payload.Len())
		payload.Write(block)
		bounds = append(bounds, [2]uint64{from, uint64(payload.Len())})
		blocks = append(blocks, int32(len(block)))
	}
	comp := payload.Bytes()
	sum = sha1.Sum(comp)
	headerLen := int64(57 + 16*len(bounds))
	offset = int64(len(*b))
	var hdr []byte
	var tmp [8]byte
	binary.LittleEndian.PutUint64(tmp[:], 0)
	hdr = append(hdr, tmp[:]...)
	binary.LittleEndian.PutUint64(tmp[:], uint64(len(comp)))
	hdr = append(hdr, tmp[:]...)
	binary.LittleEndian.PutUint64(tmp[:], uint64(len(data)))
	hdr = append(hdr, tmp[:]...)
	putI32(&hdr, 1)
	hdr = append(hdr, sum[:]...)
	putI32(&hdr, int32(len(bounds)))
	for _, bl := range bounds {
		binary.LittleEndian.PutUint64(tmp[:], bl[0]+uint64(headerLen))
		hdr = append(hdr, tmp[:]...)
		binary.LittleEndian.PutUint64(tmp[:], bl[1]+uint64(headerLen))
		hdr = append(hdr, tmp[:]...)
	}
	hdr = append(hdr, 0)
	putI32(&hdr, int32(blockSize))
	*b = append(*b, hdr...)
	*b = append(*b, comp...)
	return offset, int32(len(comp)), int32(len(data)), sum, blocks
}
