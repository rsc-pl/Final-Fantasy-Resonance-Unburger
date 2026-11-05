package iostore

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

// FIoDirectoryIndexResource:
//
//	FString   MountPoint
//	uint32    NumDirectoryEntries
//	FDirectoryIndexEntry[NumDirectoryEntries]  { uint32 Name; uint32 FirstChildEntry;
//	                                             uint32 NextSiblingEntry; int32 FileEntry; }
//	uint32    NumFileEntries
//	FIoFileIndexEntry[NumFileEntries]          { uint32 Name; uint32 NextFileEntry;
//	                                             uint32 UserData; }
//	uint32    NumStrings
//	FString[NumStrings]                        string table, referenced by index
//
// Names are indexes into the string table, and UserData is the index of the chunk that
// stores the file. Every index is validated before a path is trusted.
type dirIndexEntry struct {
	Name        uint32
	FirstChild  uint32
	NextSibling uint32
	FileEntry   int32
}

type fileIndexEntry struct {
	Name     uint32
	NextFile uint32
	UserData uint32
}

// Decoder expands one Oodle block.
type Decoder func(comp []byte, rawSize int) ([]byte, error)

func parseDirectory(dir []byte) ([]File, error) {
	pos := 0
	mount, err := readFString(dir, &pos)
	if err != nil {
		return nil, fmt.Errorf("mount point: %w", err)
	}
	if pos+4 > len(dir) {
		return nil, fmt.Errorf("truncated directory count")
	}
	dirCount := int(binary.LittleEndian.Uint32(dir[pos:]))
	pos += 4
	if dirCount <= 0 || dirCount > 1<<20 || pos+dirCount*16 > len(dir) {
		return nil, fmt.Errorf("implausible directory count %d", dirCount)
	}
	dirs := make([]dirIndexEntry, dirCount)
	for i := range dirs {
		d := dir[pos : pos+16]
		dirs[i] = dirIndexEntry{
			Name:        binary.LittleEndian.Uint32(d[0:]),
			FirstChild:  binary.LittleEndian.Uint32(d[4:]),
			NextSibling: binary.LittleEndian.Uint32(d[8:]),
			FileEntry:   int32(binary.LittleEndian.Uint32(d[12:])),
		}
		pos += 16
	}
	if pos+4 > len(dir) {
		return nil, fmt.Errorf("truncated file count")
	}
	fileCount := int(binary.LittleEndian.Uint32(dir[pos:]))
	pos += 4
	if fileCount <= 0 || fileCount > 1<<22 || pos+fileCount*12 > len(dir) {
		return nil, fmt.Errorf("implausible file count %d", fileCount)
	}
	files := make([]fileIndexEntry, fileCount)
	for i := range files {
		f := dir[pos : pos+12]
		files[i] = fileIndexEntry{
			Name:     binary.LittleEndian.Uint32(f[0:]),
			NextFile: binary.LittleEndian.Uint32(f[4:]),
			UserData: binary.LittleEndian.Uint32(f[8:]),
		}
		pos += 12
	}
	if pos+4 > len(dir) {
		return nil, fmt.Errorf("truncated string count")
	}
	strCount := int(binary.LittleEndian.Uint32(dir[pos:]))
	pos += 4
	if strCount <= 0 || strCount > 1<<22 {
		return nil, fmt.Errorf("implausible string count %d", strCount)
	}
	table := make([]string, strCount)
	for i := range table {
		s, err := readFString(dir, &pos)
		if err != nil {
			return nil, fmt.Errorf("string %d: %w", i, err)
		}
		table[i] = s
	}
	if pos != len(dir) {
		return nil, fmt.Errorf("directory index has %d trailing bytes", len(dir)-pos)
	}

	for i, d := range dirs {
		if d.Name == ^uint32(0) {
			// The root entry has no name; the mount point supplies it.
			continue
		}
		if int(d.Name) >= strCount {
			return nil, fmt.Errorf("directory %d names string %d of %d", i, d.Name, strCount)
		}
		if d.FirstChild != ^uint32(0) && int(d.FirstChild) >= dirCount {
			return nil, fmt.Errorf("directory %d first child %d of %d", i, d.FirstChild, dirCount)
		}
		if d.NextSibling != ^uint32(0) && int(d.NextSibling) >= dirCount {
			return nil, fmt.Errorf("directory %d sibling %d of %d", i, d.NextSibling, dirCount)
		}
	}
	for i, f := range files {
		if int(f.Name) >= strCount {
			return nil, fmt.Errorf("file %d names string %d of %d", i, f.Name, strCount)
		}
	}

	paths := make([]string, fileCount)
	seen := make([]bool, dirCount)
	var walk func(idx int, prefix string, depth int)
	walk = func(idx int, prefix string, depth int) {
		if depth > 128 || idx < 0 || idx >= dirCount || seen[idx] {
			return
		}
		seen[idx] = true
		d := &dirs[idx]
		name := ""
		if d.Name != ^uint32(0) && int(d.Name) < len(table) {
			name = table[d.Name]
		}
		full := prefix + name
		// A directory owns a chain of files: FileEntry is the head, NextFileEntry links on.
		if d.FileEntry >= 0 && int(d.FileEntry) < fileCount {
			dirPrefix := full
			if dirPrefix != "" && !strings.HasSuffix(dirPrefix, "/") {
				dirPrefix += "/"
			}
			guard := 0
			for f := int(d.FileEntry); f >= 0 && f < fileCount && guard < fileCount; f = int(files[f].NextFile) {
				if int(files[f].Name) < len(table) {
					paths[f] = dirPrefix + table[files[f].Name]
				}
				guard++
				if files[f].NextFile == ^uint32(0) {
					break
				}
			}
		}
		if d.FirstChild != ^uint32(0) && d.FirstChild != 0 {
			walk(int(d.FirstChild), full+"/", depth+1)
		}
		if d.NextSibling != ^uint32(0) && d.NextSibling != 0 {
			walk(int(d.NextSibling), prefix, depth)
		}
	}
	walk(0, mount, 0)

	out := make([]File, 0, fileCount)
	for i := range files {
		p := paths[i]
		if p == "" && int(files[i].Name) < len(table) {
			p = mount + table[files[i].Name]
		}
		for strings.Contains(p, "//") {
			p = strings.ReplaceAll(p, "//", "/")
		}
		out = append(out, File{Path: p, Index: int(files[i].UserData)})
	}
	return out, nil
}

func readFString(b []byte, pos *int) (string, error) {
	if *pos+4 > len(b) {
		return "", fmt.Errorf("truncated string length")
	}
	n := int(int32(binary.LittleEndian.Uint32(b[*pos:])))
	*pos += 4
	if n == 0 {
		return "", nil
	}
	if n < 0 {
		// Negative length means a UTF-16 string of -n code units including the terminator.
		units := -n
		if *pos+units*2 > len(b) {
			return "", fmt.Errorf("implausible wide string length %d", units)
		}
		raw := b[*pos : *pos+units*2]
		*pos += units * 2
		buf := make([]rune, 0, units)
		for i := 0; i+1 < len(raw); i += 2 {
			u := rune(binary.LittleEndian.Uint16(raw[i:]))
			if u == 0 {
				break
			}
			buf = append(buf, u)
		}
		return string(buf), nil
	}
	if *pos+n > len(b) {
		return "", fmt.Errorf("implausible string length %d", n)
	}
	s := string(b[*pos : *pos+n-1])
	*pos += n
	return s, nil
}

// Read returns the decoded payload of a chunk. Blocks whose stored size equals their
// declared size and whose method is 0 are copied through; the rest go to the decoder.
func (c *Container) Read(id ChunkID, decode Decoder) ([]byte, error) {
	var entry *Entry
	for i := range c.entries {
		if c.entries[i].ID == id {
			entry = &c.entries[i]
			break
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("chunk %s not found", id)
	}
	f, err := os.Open(c.UcasPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make([]byte, 0, entry.Length)
	for i, blk := range entry.Blocks {
		comp := make([]byte, blk.Compressed)
		if _, err := f.ReadAt(comp, blk.Offset); err != nil {
			return nil, fmt.Errorf("block %d: %w", i, err)
		}
		want := blk.Uncompressed
		if remain := entry.Length - int64(len(out)); remain < int64(want) {
			want = int(remain)
		}
		if blk.Compressed == want && blk.Method == 0 {
			out = append(out, comp[:want]...)
			continue
		}
		if decode == nil {
			return nil, fmt.Errorf("block %d is compressed and no decoder is configured", i)
		}
		raw, err := decode(comp, want)
		if err != nil {
			return nil, fmt.Errorf("block %d: %w", i, err)
		}
		if len(raw) < want {
			return nil, fmt.Errorf("block %d decoded %d of %d bytes", i, len(raw), want)
		}
		out = append(out, raw[:want]...)
	}
	if int64(len(out)) != entry.Length {
		return nil, fmt.Errorf("chunk %s decoded %d of %d bytes", id, len(out), entry.Length)
	}
	return out, nil
}

// ReadFile reads a file listed in the directory index.
func (c *Container) ReadFile(f File, decode Decoder) ([]byte, error) {
	return c.Read(f.ChunkID, decode)
}
