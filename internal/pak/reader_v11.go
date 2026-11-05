package pak

// Independent pass over a version 11 archive: locate the footer, verify every SHA-1 gate, parse the primary
// index, the path-hash index and the full directory index, then read a file by path through the hash table. It is
// deliberately not the writer: it re-derives everything from the bytes on disk.

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"strings"
)

// V11File is one file found in a version 11 index.
type V11File struct {
	Path         string
	Location     int32
	Offset       int64
	Size         int64
	Uncompressed int64
	Method       uint32
	SHA1         string
}

// V11Index is what a verification pass established.
type V11Index struct {
	MountPoint      string
	NumEntries      int32
	PathHashSeed    uint64
	PathHashCount   int32
	PathHashMatch   int
	FullDirFiles    int
	EncodedSize     int32
	PrimarySHA1     string
	PathHashSHA1    string
	FullDirSHA1     string
	FooterIndexSHA1 string
	Files           []V11File
	Compressed      int
	Problems        []string
}

// ReadV11 parses an archive and returns its index together with every problem it found.
func ReadV11(p string) (*V11Index, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if len(raw) < FooterSizeV11 {
		return nil, fmt.Errorf("pak: file is shorter than a version 11 footer")
	}
	magicAt := -1
	for i := len(raw) - 4; i >= len(raw)-256 && i >= 0; i-- {
		if binary.LittleEndian.Uint32(raw[i:]) == Magic {
			magicAt = i
			break
		}
	}
	if magicAt < 0 {
		return nil, fmt.Errorf("pak: no magic in the tail")
	}
	footerStart := magicAt - 17
	if footerStart < 0 || len(raw)-footerStart != FooterSizeV11 {
		return nil, fmt.Errorf("pak: footer is %d bytes, want %d", len(raw)-footerStart, FooterSizeV11)
	}
	encFlag := raw[magicAt-1]
	if encFlag != 0 {
		return nil, fmt.Errorf("pak: index encryption is not supported")
	}
	version := int32(binary.LittleEndian.Uint32(raw[magicAt+4:]))
	if version != Version11 {
		return nil, fmt.Errorf("pak: version %d, want %d", version, Version11)
	}
	idxOff := int64(binary.LittleEndian.Uint64(raw[magicAt+8:]))
	idxSize := int64(binary.LittleEndian.Uint64(raw[magicAt+16:]))
	var footerHash [20]byte
	copy(footerHash[:], raw[magicAt+24:magicAt+44])
	if idxOff < 0 || idxSize < 0 || idxOff+idxSize > int64(len(raw)) {
		return nil, fmt.Errorf("pak: index region outside the file")
	}
	primary := raw[idxOff : idxOff+idxSize]
	out := &V11Index{FooterIndexSHA1: fmt.Sprintf("%x", footerHash), PrimarySHA1: fmt.Sprintf("%x", sha1.Sum(primary))}
	if sha1.Sum(primary) != footerHash {
		out.Problems = append(out.Problems, "primary index sha1 does not match the footer")
	}
	pos := 0
	mp, ok := readFStringV11(primary, &pos)
	if !ok {
		return nil, fmt.Errorf("pak: cannot read the mount point")
	}
	out.MountPoint = mp
	out.NumEntries = int32(binary.LittleEndian.Uint32(primary[pos:]))
	pos += 4
	out.PathHashSeed = binary.LittleEndian.Uint64(primary[pos:])
	pos += 8
	hasPH := int32(binary.LittleEndian.Uint32(primary[pos:]))
	pos += 4
	if hasPH != 1 {
		return nil, fmt.Errorf("pak: no path hash index, which this reader requires")
	}
	phOff := int64(binary.LittleEndian.Uint64(primary[pos:]))
	pos += 8
	phSize := int64(binary.LittleEndian.Uint64(primary[pos:]))
	pos += 8
	var phHash [20]byte
	copy(phHash[:], primary[pos:pos+20])
	pos += 20
	hasFD := int32(binary.LittleEndian.Uint32(primary[pos:]))
	pos += 4
	if hasFD != 1 {
		return nil, fmt.Errorf("pak: no full directory index, which this reader requires")
	}
	fdOff := int64(binary.LittleEndian.Uint64(primary[pos:]))
	pos += 8
	fdSize := int64(binary.LittleEndian.Uint64(primary[pos:]))
	pos += 8
	var fdHash [20]byte
	copy(fdHash[:], primary[pos:pos+20])
	pos += 20
	out.EncodedSize = int32(binary.LittleEndian.Uint32(primary[pos:]))
	pos += 4
	encoded := primary[pos : pos+int(out.EncodedSize)]
	pos += int(out.EncodedSize)
	nonEncoded := int32(binary.LittleEndian.Uint32(primary[pos:]))
	pos += 4
	if nonEncoded != 0 {
		return nil, fmt.Errorf("pak: %d non-encoded entries are not supported", nonEncoded)
	}
	if pos != len(primary) {
		out.Problems = append(out.Problems, fmt.Sprintf("primary index left %d bytes unread", len(primary)-pos))
	}

	ph := raw[phOff : phOff+phSize]
	fd := raw[fdOff : fdOff+fdSize]
	out.PathHashSHA1 = fmt.Sprintf("%x", sha1.Sum(ph))
	out.FullDirSHA1 = fmt.Sprintf("%x", sha1.Sum(fd))
	if sha1.Sum(ph) != phHash {
		out.Problems = append(out.Problems, "path hash index sha1 does not match the primary index")
	}
	if sha1.Sum(fd) != fdHash {
		out.Problems = append(out.Problems, "full directory index sha1 does not match the primary index")
	}

	// Directory index: names and locations.
	dirs, ok := parseDirectoryV11(fd)
	if !ok {
		return nil, fmt.Errorf("pak: cannot parse the full directory index")
	}
	var files []V11File
	for _, d := range dirs {
		for _, f := range d.files {
			files = append(files, V11File{Path: d.name + f.name, Location: f.loc})
		}
	}
	out.FullDirFiles = len(files)

	// Every location must resolve to a 12-byte stored record inside the encoded blob.
	for i := range files {
		f := &files[i]
		if f.Location < 0 || int(f.Location)+12 > len(encoded) {
			out.Problems = append(out.Problems, fmt.Sprintf("%s: entry location outside the encoded blob", f.Path))
			continue
		}
		e, ok := decodeRecord(encoded[f.Location:])
		if !ok {
			out.Problems = append(out.Problems, fmt.Sprintf("%s: unsupported encoded entry", f.Path))
			continue
		}
		f.Offset = e.offset
		f.Uncompressed = e.uncompressed
		f.Size = e.size
		f.Method = e.method
	}

	// Path hash table must agree with the directory index.
	count := int32(binary.LittleEndian.Uint32(ph[:]))
	out.PathHashCount = count
	table := map[uint64]int32{}
	hp := 4
	for i := int32(0); i < count; i++ {
		if hp+12 > len(ph) {
			out.Problems = append(out.Problems, "path hash table is truncated")
			break
		}
		table[binary.LittleEndian.Uint64(ph[hp:])] = int32(binary.LittleEndian.Uint32(ph[hp+8:]))
		hp += 12
	}
	for _, f := range files {
		h := PathHash(out.PathHashSeed, f.Path)
		if loc, ok := table[h]; ok && loc == f.Location {
			out.PathHashMatch++
		} else {
			out.Problems = append(out.Problems, fmt.Sprintf("%s: path hash does not resolve to its entry", f.Path))
		}
	}
	// Compression is expected in the shipped archives; the reader reports it, it does not fail on it.
	compressed := 0
	for _, f := range files {
		if f.Method != 0 {
			compressed++
		}
	}
	out.Compressed = compressed

	// Read each payload through its local header and check the on-disk hash. Stored and compressed entries
	// both carry a SHA-1 of the bytes actually written, so an integrity consumer gets a value for either.
	for i := range files {
		f := &files[i]
		var (
			sum string
			err error
		)
		if f.Method == 0 {
			sum, err = verifyLocal(raw, f)
		} else {
			sum, err = verifyCompressedLocal(raw, f)
		}
		if err != nil {
			out.Problems = append(out.Problems, fmt.Sprintf("%s: %v", f.Path, err))
			continue
		}
		f.SHA1 = sum
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	out.Files = files
	return out, nil
}

// decodedRecord is one bit-packed entry record.
type decodedRecord struct {
	offset       int64
	size         int64
	uncompressed int64
	method       uint32
}

// decodeRecord parses a bit-packed entry record. Field widths are driven by the flags word, which is what makes a
// reader agree with a writer that always emits the 32-bit stored form.
func decodeRecord(b []byte) (decodedRecord, bool) {
	if len(b) < 4 {
		return decodedRecord{}, false
	}
	var out decodedRecord
	flags := binary.LittleEndian.Uint32(b)
	pos := 4
	read := func() (int64, bool) {
		if pos+8 > len(b) {
			return 0, false
		}
		v := int64(binary.LittleEndian.Uint64(b[pos:]))
		pos += 8
		return v, true
	}
	read32 := func() (int64, bool) {
		if pos+4 > len(b) {
			return 0, false
		}
		v := int64(binary.LittleEndian.Uint32(b[pos:]))
		pos += 4
		return v, true
	}
	out.method = (flags >> 23) & 0x3F
	blockSizeBits := flags & 0x3F
	if blockSizeBits == 0x3F {
		if _, ok := read32(); !ok {
			return out, false
		}
	}
	var ok bool
	if flags&(1<<31) != 0 {
		out.offset, ok = read32()
	} else {
		out.offset, ok = read()
	}
	if !ok {
		return out, false
	}
	if flags&(1<<30) != 0 {
		out.uncompressed, ok = read32()
	} else {
		out.uncompressed, ok = read()
	}
	if !ok {
		return out, false
	}
	if out.method == 0 {
		out.size = out.uncompressed
		return out, true
	}
	if flags&(1<<29) != 0 {
		out.size, ok = read32()
	} else {
		out.size, ok = read()
	}
	return out, ok
}

// verifyLocal checks the inline header of one entry and the hash of its payload.
func verifyLocal(raw []byte, f *V11File) (string, error) {
	if f.Offset < 0 || f.Offset+53 > int64(len(raw)) {
		return "", fmt.Errorf("entry offset outside the file")
	}
	h := raw[f.Offset : f.Offset+53]
	lSize := int64(binary.LittleEndian.Uint64(h[8:]))
	lUnc := int64(binary.LittleEndian.Uint64(h[16:]))
	lMethod := binary.LittleEndian.Uint32(h[24:])
	var lHash [20]byte
	copy(lHash[:], h[28:48])
	if lMethod != 0 {
		return "", fmt.Errorf("local header declares compression method %d", lMethod)
	}
	if lSize != f.Size || lUnc != f.Size {
		return "", fmt.Errorf("local header size %d/%d disagrees with the index (%d)", lSize, lUnc, f.Size)
	}
	start := f.Offset + 53
	if start+f.Size > int64(len(raw)) {
		return "", fmt.Errorf("payload outside the file")
	}
	payload := raw[start : start+f.Size]
	sum := sha1.Sum(payload)
	if sum != lHash {
		return "", fmt.Errorf("payload sha1 does not match the local header")
	}
	return fmt.Sprintf("%x", sum), nil
}

// verifyCompressedLocal checks the inline header of a compressed entry and hashes the compressed payload it
// writes, which is the value the header and a caller both need for integrity. It also checks that the block table
// covers the payload exactly, so a truncated or overlapping block range is reported instead of accepted.
func verifyCompressedLocal(raw []byte, f *V11File) (string, error) {
	if f.Offset < 0 || f.Offset+57 > int64(len(raw)) {
		return "", fmt.Errorf("compressed entry header outside the file")
	}
	h := raw[f.Offset : f.Offset+57]
	lSize := int64(binary.LittleEndian.Uint64(h[8:]))
	lUnc := int64(binary.LittleEndian.Uint64(h[16:]))
	lMethod := binary.LittleEndian.Uint32(h[24:])
	var lHash [20]byte
	copy(lHash[:], h[28:48])
	blocks := int64(int32(binary.LittleEndian.Uint32(h[48:])))
	if lMethod == 0 {
		return "", fmt.Errorf("compressed entry declares method 0")
	}
	if lMethod != f.Method {
		return "", fmt.Errorf("local header declares compression method %d, index declares %d", lMethod, f.Method)
	}
	if lSize != f.Size || lUnc != f.Uncompressed {
		return "", fmt.Errorf("local header size %d/%d disagrees with the index (%d/%d)", lSize, lUnc, f.Size, f.Uncompressed)
	}
	if blocks <= 0 || blocks > 1<<20 {
		return "", fmt.Errorf("compressed entry declares %d blocks", blocks)
	}
	headerLen := int64(57 + 16*blocks)
	if f.Offset+headerLen > int64(len(raw)) {
		return "", fmt.Errorf("block table outside the file")
	}
	prevEnd := headerLen
	for i := int64(0); i < blocks; i++ {
		rec := f.Offset + 52 + 16*i
		start := int64(binary.LittleEndian.Uint64(raw[rec:]))
		end := int64(binary.LittleEndian.Uint64(raw[rec+8:]))
		if start != prevEnd || end <= start {
			return "", fmt.Errorf("block %d range %d..%d does not continue the payload", i, start, end)
		}
		if end > headerLen+f.Size {
			return "", fmt.Errorf("block %d ends outside the payload", i)
		}
		prevEnd = end
	}
	if prevEnd != headerLen+f.Size {
		return "", fmt.Errorf("block table covers %d of %d compressed bytes", prevEnd-headerLen, f.Size)
	}
	start := f.Offset + headerLen
	payload := raw[start : start+f.Size]
	sum := sha1.Sum(payload)
	if sum != lHash {
		return "", fmt.Errorf("payload sha1 does not match the local header")
	}
	return fmt.Sprintf("%x", sum), nil
}

// ReadV11File returns the payload of one path, resolved through the path-hash table.
func ReadV11File(p, want string) ([]byte, error) {
	idx, err := ReadV11(p)
	if err != nil {
		return nil, err
	}
	want = strings.TrimPrefix(strings.ReplaceAll(want, "\\", "/"), "/")
	for _, f := range idx.Files {
		if f.Path == want {
			raw, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			start := f.Offset + 53
			return raw[start : start+f.Size], nil
		}
	}
	return nil, fmt.Errorf("pak: %s not found", want)
}

type dirV11 struct {
	name  string
	files []struct {
		name string
		loc  int32
	}
}

func parseDirectoryV11(b []byte) ([]dirV11, bool) {
	pos := 0
	if len(b) < 4 {
		return nil, false
	}
	n := int32(binary.LittleEndian.Uint32(b[pos:]))
	pos += 4
	if n < 0 || n > 1_000_000 {
		return nil, false
	}
	out := make([]dirV11, 0, n)
	for i := int32(0); i < n; i++ {
		name, ok := readFStringV11(b, &pos)
		if !ok {
			return nil, false
		}
		d := dirV11{name: name}
		if pos+4 > len(b) {
			return nil, false
		}
		fc := int32(binary.LittleEndian.Uint32(b[pos:]))
		pos += 4
		if fc < 0 || fc > 1_000_000 {
			return nil, false
		}
		for j := int32(0); j < fc; j++ {
			fn, ok := readFStringV11(b, &pos)
			if !ok {
				return nil, false
			}
			if pos+4 > len(b) {
				return nil, false
			}
			loc := int32(binary.LittleEndian.Uint32(b[pos:]))
			pos += 4
			d.files = append(d.files, struct {
				name string
				loc  int32
			}{fn, loc})
		}
		out = append(out, d)
	}
	return out, pos == len(b)
}

func readFStringV11(b []byte, pos *int) (string, bool) {
	if *pos+4 > len(b) {
		return "", false
	}
	n := int32(binary.LittleEndian.Uint32(b[*pos:]))
	*pos += 4
	if n == 0 {
		return "", true
	}
	if n > 0 {
		if *pos+int(n) > len(b) {
			return "", false
		}
		s := string(b[*pos : *pos+int(n)-1])
		*pos += int(n)
		return s, true
	}
	return "", false
}

// PayloadDecoder expands one compression block. It is supplied by the caller so the reader stays independent of
// any particular codec.
type PayloadDecoder func(comp []byte, rawSize int) ([]byte, error)

// ReadV11Decoded returns the payload of one path, decompressing the entry's blocks when it is compressed.
//
// A compressed entry in these archives is laid out as: offset, size of the compressed payload, uncompressed
// size, method index, sha1 of the compressed payload, a block count, one (start, end) pair per block measured
// from the start of the entry, a flags byte and the uncompressed block size. The payload follows immediately.
func ReadV11Decoded(p, want string, dec PayloadDecoder) ([]byte, error) {
	idx, err := ReadV11(p)
	if err != nil {
		return nil, err
	}
	var target *V11File
	for i := range idx.Files {
		if idx.Files[i].Path == strings.TrimPrefix(strings.ReplaceAll(want, "\\", "/"), "/") {
			target = &idx.Files[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("pak: %s not found", want)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if target.Method == 0 {
		start := target.Offset + 53
		return raw[start : start+target.Size], nil
	}
	if dec == nil {
		return nil, fmt.Errorf("pak: %s is compressed and no decoder was supplied", want)
	}
	if target.Offset+52 > int64(len(raw)) {
		return nil, fmt.Errorf("pak: %s header outside the file", want)
	}
	blocks := int(int32(binary.LittleEndian.Uint32(raw[target.Offset+48:])))
	if blocks <= 0 || blocks > 1<<20 {
		return nil, fmt.Errorf("pak: %s declares %d blocks", want, blocks)
	}
	headerLen := int64(57 + 16*blocks)
	if target.Offset+headerLen > int64(len(raw)) {
		return nil, fmt.Errorf("pak: %s block table outside the file", want)
	}
	blockSize := int(binary.LittleEndian.Uint32(raw[target.Offset+53+16*int64(blocks):]))
	if blockSize <= 0 {
		blockSize = DefaultBlockSize
	}
	out := make([]byte, 0, target.Uncompressed)
	for i := 0; i < blocks; i++ {
		rec := target.Offset + 52 + int64(16*i)
		start := int64(binary.LittleEndian.Uint64(raw[rec:]))
		end := int64(binary.LittleEndian.Uint64(raw[rec+8:]))
		if end <= start || target.Offset+end > int64(len(raw)) {
			return nil, fmt.Errorf("pak: %s block %d is outside the file", want, i)
		}
		remain := int(target.Uncompressed) - len(out)
		size := blockSize
		if remain < size {
			size = remain
		}
		chunk := raw[target.Offset+start : target.Offset+end]
		b, derr := dec(chunk, size)
		if derr != nil {
			return nil, fmt.Errorf("pak: %s block %d: %w", target.Path, i, derr)
		}
		out = append(out, b...)
	}
	if int64(len(out)) != target.Uncompressed {
		return nil, fmt.Errorf("pak: %s decoded to %d bytes, want %d", want, len(out), target.Uncompressed)
	}
	return out, nil
}

// blockSizeOf turns the flags byte and block-size field of an entry header into a byte count.
func blockSizeOf(flags uint8, size uint32) uint32 {
	if size != 0 {
		return size
	}
	return uint32(1) << 16
}
