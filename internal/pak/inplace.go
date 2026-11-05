package pak

// A working localisation mod for this title does not add a second archive: it replaces FFRS-Switch.pak with a
// re-written copy of itself. That path needs no directory discovery and no mount priority, so this file rebuilds
// an archive in place — every other entry is copied byte for byte, only the target entry and the three index
// regions are regenerated.

import (
	"bufio"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// InPlaceRequest describes a re-write of one archive.
type InPlaceRequest struct {
	Src         string
	Dst         string
	Backup      string // written before Dst is replaced when Dst == Src
	VirtualPath string
	Payload     []byte
	// KnownSHA256 lists accepted digests of the source archive. An unknown or already modified input is refused.
	KnownSHA256 []string
}

// InPlaceResult reports what was verified after the re-write.
type InPlaceResult struct {
	SrcSHA256    string
	DstSHA256    string
	SrcBytes     int64
	DstBytes     int64
	Entries      int
	Copied       int
	CopiedBytes  int64
	TargetOld    int64
	TargetNew    int64
	PrimarySHA1  string
	PathHashSHA1 string
	FullDirSHA1  string
	TargetSHA1   string
	Problems     []string
}

// FileSHA256 streams a digest.
func FileSHA256(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), n, nil
}

// blobSpan is one entry's on-disk extent: inline header, payload and any compression block table.
type blobSpan struct {
	path       string
	start      int64
	end        int64
	method     uint32
	flags      uint32
	size       int64
	unc        int64
	compressed bool
}

// PatchArchive rebuilds an archive with one entry replaced, then verifies the result against the source.
func PatchArchive(req InPlaceRequest) (InPlaceResult, error) {
	var res InPlaceResult
	if req.VirtualPath == "" || len(req.Payload) == 0 {
		return res, fmt.Errorf("pak: a virtual path and a payload are required")
	}
	sum, n, err := FileSHA256(req.Src)
	if err != nil {
		return res, err
	}
	res.SrcSHA256 = sum
	res.SrcBytes = n
	if len(req.KnownSHA256) > 0 {
		ok := false
		for _, k := range req.KnownSHA256 {
			if k == sum {
				ok = true
			}
		}
		if !ok {
			return res, fmt.Errorf("pak: source sha256 %s is neither a known original nor a supported build", sum)
		}
	}
	f, err := os.Open(req.Src)
	if err != nil {
		return res, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return res, err
	}
	idx, err := ReadV11(req.Src)
	if err != nil {
		return res, err
	}
	if len(idx.Problems) > 0 {
		return res, fmt.Errorf("pak: source index reports %d problems, first: %s", len(idx.Problems), idx.Problems[0])
	}
	idxOff := int64(0)
	{
		raw := make([]byte, 44)
		if _, err := f.ReadAt(raw, st.Size()-FooterSizeV11+17); err != nil {
			return res, err
		}
		if binary.LittleEndian.Uint32(raw[4:]) != uint32(Version11) {
			return res, fmt.Errorf("pak: unsupported version in the footer")
		}
		idxOff = int64(binary.LittleEndian.Uint64(raw[8:]))
	}

	// One span per entry, in file order, covering every byte before the primary index.
	spans := make([]blobSpan, 0, len(idx.Files))
	for _, e := range idx.Files {
		spans = append(spans, blobSpan{
			path: e.Path, start: e.Offset, method: e.Method,
			size: e.Size, unc: e.Uncompressed, compressed: e.Method != 0,
		})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	for i := range spans {
		if i+1 < len(spans) {
			spans[i].end = spans[i+1].start
		} else {
			spans[i].end = idxOff
		}
		if spans[i].end <= spans[i].start {
			return res, fmt.Errorf("pak: entry %q has no bytes", spans[i].path)
		}
	}
	res.Entries = len(spans)
	target := -1
	for i := range spans {
		if spans[i].path == req.VirtualPath {
			target = i
			break
		}
	}
	if target < 0 {
		return res, fmt.Errorf("pak: %s is not in the source archive", req.VirtualPath)
	}
	res.TargetOld = spans[target].end - spans[target].start
	res.TargetNew = int64(len(req.Payload)) + 53

	if err := os.MkdirAll(filepath.Dir(req.Dst), 0o755); err != nil {
		return res, err
	}
	tmp := req.Dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return res, err
	}
	defer func() {
		out.Close()
		if err != nil {
			os.Remove(tmp)
		}
	}()
	w := bufio.NewWriterSize(out, 1<<20)
	entryOrder := make([]WrittenEntry, len(spans))
	var pos int64
	buf := make([]byte, 1<<20)
	for i := range spans {
		sp := &spans[i]
		if sp.path == req.VirtualPath {
			sum := sha1.Sum(req.Payload)
			var hdr [53]byte
			binary.LittleEndian.PutUint64(hdr[0:], 0)
			binary.LittleEndian.PutUint64(hdr[8:], uint64(len(req.Payload)))
			binary.LittleEndian.PutUint64(hdr[16:], uint64(len(req.Payload)))
			binary.LittleEndian.PutUint32(hdr[24:], 0)
			copy(hdr[28:], sum[:])
			if _, werr := w.Write(hdr[:]); werr != nil {
				err = werr
				return res, werr
			}
			if _, werr := w.Write(req.Payload); werr != nil {
				err = werr
				return res, werr
			}
			entryOrder[i] = WrittenEntry{
				Path: sp.path, Offset: pos, Size: int32(len(req.Payload)),
				Uncompressed: int32(len(req.Payload)), SHA1: fmt.Sprintf("%x", sum),
			}
			pos += 53 + int64(len(req.Payload))
			res.TargetSHA1 = entryOrder[i].SHA1
			continue
		}
		// Copied byte for byte, header included: the local offset field is always zero in the shipped archives,
		// so a moved entry needs no header edit.
		remaining := sp.end - sp.start
		var off int64
		for remaining > 0 {
			chunk := int64(len(buf))
			if remaining < chunk {
				chunk = remaining
			}
			if _, rerr := f.ReadAt(buf[:chunk], sp.start+off); rerr != nil && rerr != io.EOF {
				err = rerr
				return res, rerr
			}
			if _, werr := w.Write(buf[:chunk]); werr != nil {
				err = werr
				return res, werr
			}
			off += chunk
			remaining -= chunk
		}
		entryOrder[i] = WrittenEntry{
			Path: sp.path, Offset: pos, Size: int32(sp.size), Method: sp.method,
			Uncompressed: int32(sp.unc), Compressed: sp.compressed,
		}
		pos += sp.end - sp.start
		res.Copied++
		res.CopiedBytes += sp.end - sp.start
	}

	// Rebuild the three index regions over the same entry set with the new offsets.
	for i := range entryOrder {
		entryOrder[i].PathHash = PathHash(idx.PathHashSeed, entryOrder[i].Path)
	}
	encoded := encodeEntries(entryOrder)
	fullDir := buildFullDirectory(entryOrder)
	pathHash := buildPathHash(entryOrder, idx.PathHashSeed)

	primary := buildPrimary(idx.MountPoint, idx.PathHashSeed, int32(len(entryOrder)), encoded, 0, 0, [20]byte{}, 0, 0, [20]byte{})
	primaryOffset := pos
	pathHashOffset := primaryOffset + int64(len(primary))
	fullDirOffset := pathHashOffset + int64(len(pathHash))
	primary = buildPrimary(idx.MountPoint, idx.PathHashSeed, int32(len(entryOrder)), encoded,
		pathHashOffset, int64(len(pathHash)), sha1.Sum(pathHash),
		fullDirOffset, int64(len(fullDir)), sha1.Sum(fullDir))
	if _, werr := w.Write(primary); werr != nil {
		err = werr
		return res, werr
	}
	if _, werr := w.Write(pathHash); werr != nil {
		err = werr
		return res, werr
	}
	if _, werr := w.Write(fullDir); werr != nil {
		err = werr
		return res, werr
	}
	if _, werr := w.Write(buildFooter(int64(len(primary)), primaryOffset, sha1.Sum(primary), "")); werr != nil {
		err = werr
		return res, werr
	}
	if werr := w.Flush(); werr != nil {
		err = werr
		return res, werr
	}
	if cerr := out.Close(); cerr != nil {
		err = cerr
		return res, cerr
	}

	// Independent read-back: the index must parse, the target must carry the new payload, and every other entry
	// must still be the same bytes as in the source.
	back, err := ReadV11(tmp)
	if err != nil {
		return res, fmt.Errorf("written archive does not parse: %w", err)
	}
	if len(back.Problems) > 0 {
		return res, fmt.Errorf("written archive reports %d problems, first: %s", len(back.Problems), back.Problems[0])
	}
	if len(back.Files) != len(spans) {
		return res, fmt.Errorf("written archive holds %d entries, source had %d", len(back.Files), len(spans))
	}
	res.PrimarySHA1 = back.PrimarySHA1
	res.PathHashSHA1 = back.PathHashSHA1
	res.FullDirSHA1 = back.FullDirSHA1
	got, err := ReadV11File(tmp, req.VirtualPath)
	if err != nil {
		return res, err
	}
	if string(got) != string(req.Payload) {
		return res, fmt.Errorf("the replaced resource does not read back identical (got %d bytes, wrote %d)", len(got), len(req.Payload))
	}

	origSums := map[string]string{}
	for i := range spans {
		sp := &spans[i]
		h, herr := blobSHA256(f, sp.start, sp.end-sp.start)
		if herr != nil {
			return res, herr
		}
		origSums[sp.path] = h
	}
	newFile, err := os.Open(tmp)
	if err != nil {
		return res, err
	}
	defer newFile.Close()
	newSums := map[string]string{}
	for _, e := range back.Files {
		sp := spanOf(spans, e.Path)
		if sp == nil {
			res.Problems = append(res.Problems, "entry missing from the source: "+e.Path)
			continue
		}
		if e.Path == req.VirtualPath {
			continue
		}
		h, herr := blobSHA256(newFile, e.Offset, sp.end-sp.start)
		if herr != nil {
			return res, herr
		}
		newSums[e.Path] = h
		if h != origSums[e.Path] {
			res.Problems = append(res.Problems, "copied bytes differ: "+e.Path)
		}
	}
	if len(res.Problems) > 0 {
		return res, fmt.Errorf("verification failed: %s", res.Problems[0])
	}

	if req.Dst == req.Src {
		if req.Backup == "" {
			return res, fmt.Errorf("pak: an in-place write needs a backup path")
		}
		if _, berr := copyFile(req.Src, req.Backup); berr != nil {
			return res, berr
		}
	}
	if rerr := os.Rename(tmp, req.Dst); rerr != nil {
		return res, rerr
	}
	res.DstSHA256, res.DstBytes, err = FileSHA256(req.Dst)
	return res, err
}

func spanOf(spans []blobSpan, p string) *blobSpan {
	for i := range spans {
		if spans[i].path == p {
			return &spans[i]
		}
	}
	return nil
}

func blobSHA256(f *os.File, off, n int64) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, off, n)); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	n, err := io.Copy(out, in)
	if err != nil {
		return n, err
	}
	if err := out.Sync(); err != nil {
		return n, err
	}
	return n, out.Close()
}

// encodeEntries writes one bit-packed record per entry, keeping each entry's own method and block-size flags and
// using 32-bit fields throughout, which every archive of this size can express.
func encodeEntries(entries []WrittenEntry) []byte {
	b := make([]byte, 0, len(entries)*16)
	for i := range entries {
		e := &entries[i]
		flags := uint32(0xE0000000)
		flags |= (e.Method & 0x3F) << 23
		var rec [16]byte
		binary.LittleEndian.PutUint32(rec[0:], flags)
		binary.LittleEndian.PutUint32(rec[4:], uint32(e.Offset))
		binary.LittleEndian.PutUint32(rec[8:], uint32(e.Uncompressed))
		n := 12
		if e.Compressed {
			binary.LittleEndian.PutUint32(rec[12:], uint32(e.Size))
			n = 16
		}
		e.Location = int32(len(b))
		b = append(b, rec[:n]...)
	}
	return b
}

func buildPathHash(entries []WrittenEntry, seed uint64) []byte {
	sorted := make([]WrittenEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].PathHash < sorted[j].PathHash })
	b := make([]byte, 0, 4+len(sorted)*12+4)
	var cnt [4]byte
	binary.LittleEndian.PutUint32(cnt[:], uint32(int32(len(sorted))))
	b = append(b, cnt[:]...)
	for _, e := range sorted {
		var pair [12]byte
		binary.LittleEndian.PutUint64(pair[0:], e.PathHash)
		binary.LittleEndian.PutUint32(pair[8:], uint32(e.Location))
		b = append(b, pair[:]...)
	}
	var zero [4]byte
	return append(b, zero[:]...)
}
