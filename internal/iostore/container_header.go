package iostore

// FIoContainerHeader is the payload of the ContainerHeader chunk (type 6). The engine reads it to learn which
// packages a container holds. Version 5 is the layout this title uses.
//
// The leading arrays, the serial info and the soft-reference flag are parsed field by field. The middle region
// (redirect name map, localised packages, package redirects) is name-map driven; this reader captures it verbatim
// and requires the round trip to reproduce it byte for byte, rather than pretending to decode names it does not.

import (
	"encoding/binary"
	"fmt"
)

// ContainerHeaderMagic starts every FIoContainerHeader.
const ContainerHeaderMagic uint32 = 0x496F436E

// ContainerHeaderVersionSoftRefs is the version this title ships.
const ContainerHeaderVersionSoftRefs uint32 = 5

// PackageID is the 12-byte package identity used by the store.
type PackageID [12]byte

// ContainerHeader is a parsed header. Middle holds the redirect/name-map region verbatim.
type ContainerHeader struct {
	Version       uint32
	ContainerID   uint64
	PackageIDs    []PackageID
	StoreEntries  []byte
	OptionalIDs   []PackageID
	OptionalStore []byte
	Middle        []byte
	SoftRefOffset int64
	SoftRefSize   int64
	HasSoftRefs   bool
	// FlagAfterWindow records the variant where the four-byte flag follows the soft-reference window instead of
	// sitting inside it. Both occur in real containers, so the parser records which one it read.
	FlagAfterWindow bool
}

// ParseContainerHeader reads a header and refuses anything it does not fully understand.
func ParseContainerHeader(b []byte) (*ContainerHeader, error) {
	if len(b) < 16 {
		return nil, fmt.Errorf("iostore: container header is %d bytes, need at least 16", len(b))
	}
	if binary.LittleEndian.Uint32(b) != ContainerHeaderMagic {
		return nil, fmt.Errorf("iostore: bad container header magic %08x", binary.LittleEndian.Uint32(b))
	}
	h := &ContainerHeader{Version: binary.LittleEndian.Uint32(b[4:])}
	if h.Version != ContainerHeaderVersionSoftRefs {
		return nil, fmt.Errorf("iostore: container header version %d is not supported", h.Version)
	}
	h.ContainerID = binary.LittleEndian.Uint64(b[8:])
	pos := 16
	readIDs := func() ([]PackageID, error) {
		if pos+4 > len(b) {
			return nil, fmt.Errorf("iostore: truncated package id count at %d", pos)
		}
		n := int(int32(binary.LittleEndian.Uint32(b[pos:])))
		pos += 4
		if n < 0 || pos+n*12 > len(b) {
			return nil, fmt.Errorf("iostore: %d package ids do not fit", n)
		}
		out := make([]PackageID, n)
		for i := 0; i < n; i++ {
			copy(out[i][:], b[pos:pos+12])
			pos += 12
		}
		return out, nil
	}
	readBuf := func() ([]byte, error) {
		if pos+4 > len(b) {
			return nil, fmt.Errorf("iostore: truncated buffer length at %d", pos)
		}
		n := int(int32(binary.LittleEndian.Uint32(b[pos:])))
		pos += 4
		if n < 0 || pos+n > len(b) {
			return nil, fmt.Errorf("iostore: buffer of %d bytes does not fit", n)
		}
		out := b[pos : pos+n]
		pos += n
		return out, nil
	}
	var err error
	if h.PackageIDs, err = readIDs(); err != nil {
		return nil, err
	}
	if h.StoreEntries, err = readBuf(); err != nil {
		return nil, err
	}
	if h.OptionalIDs, err = readIDs(); err != nil {
		return nil, err
	}
	if h.OptionalStore, err = readBuf(); err != nil {
		return nil, err
	}
	// Serial info sits immediately before the soft-reference window it describes, so its position is found by
	// walking back from the end: the window either contains the four-byte flag (offset+size == len) or is followed
	// by it (offset+size+4 == len). Whichever arithmetic matches the declared values is the one that was written.
	if len(b) < pos+20 {
		return nil, fmt.Errorf("iostore: header ends before its serial info")
	}
	found := false
	for at := len(b) - 20; at >= pos; at-- {
		off := int64(binary.LittleEndian.Uint64(b[at:]))
		size := int64(binary.LittleEndian.Uint64(b[at+8:]))
		if off != int64(at)+16 || size < 4 || size > int64(len(b)) {
			continue
		}
		after := int64(len(b)) - (int64(at) + 16 + size)
		if after != 0 && after != 4 {
			continue
		}
		h.SoftRefOffset, h.SoftRefSize = off, size
		h.FlagAfterWindow = after == 4
		h.Middle = b[pos:at]
		if h.FlagAfterWindow {
			h.HasSoftRefs = binary.LittleEndian.Uint32(b[len(b)-4:]) != 0
		} else {
			h.HasSoftRefs = binary.LittleEndian.Uint32(b[at+16:]) != 0
		}
		found = true
		break
	}
	if !found {
		return nil, fmt.Errorf("iostore: no serial info consistent with the %d-byte header", len(b))
	}
	return h, nil
}

// Serialize writes the header back. For a header parsed from a container the result is byte identical, because
// every region is written from what was read.
func (h *ContainerHeader) Serialize() []byte {
	var b []byte
	var buf [8]byte
	binary.LittleEndian.PutUint32(buf[:4], ContainerHeaderMagic)
	b = append(b, buf[:4]...)
	binary.LittleEndian.PutUint32(buf[:4], h.Version)
	b = append(b, buf[:4]...)
	binary.LittleEndian.PutUint64(buf[:], h.ContainerID)
	b = append(b, buf[:]...)
	putIDs := func(ids []PackageID) {
		binary.LittleEndian.PutUint32(buf[:4], uint32(len(ids)))
		b = append(b, buf[:4]...)
		for _, id := range ids {
			b = append(b, id[:]...)
		}
	}
	putBuf := func(p []byte) {
		binary.LittleEndian.PutUint32(buf[:4], uint32(len(p)))
		b = append(b, buf[:4]...)
		b = append(b, p...)
	}
	putIDs(h.PackageIDs)
	putBuf(h.StoreEntries)
	putIDs(h.OptionalIDs)
	putBuf(h.OptionalStore)
	b = append(b, h.Middle...)
	binary.LittleEndian.PutUint64(buf[:], uint64(h.SoftRefOffset))
	b = append(b, buf[:]...)
	binary.LittleEndian.PutUint64(buf[:], uint64(h.SoftRefSize))
	b = append(b, buf[:]...)
	// The window holds the flag in its first four bytes, as the engine's own serializer writes it.
	var flag uint32
	if h.HasSoftRefs {
		flag = 1
	}
	binary.LittleEndian.PutUint32(buf[:4], flag)
	b = append(b, buf[:4]...)
	if h.SoftRefSize > 4 {
		b = append(b, make([]byte, h.SoftRefSize-4)...)
	}
	if h.FlagAfterWindow {
		b = append(b, buf[:4]...)
	}
	return b
}

// NewEmptyContainerHeader builds the header of a container that holds no packages. Every array is empty, no
// redirects exist and no soft references are stored, so the serial info points at the four reserved bytes that
// sit immediately before the flag.
func NewEmptyContainerHeader(containerID uint64) *ContainerHeader {
	h := &ContainerHeader{
		Version:     ContainerHeaderVersionSoftRefs,
		ContainerID: containerID,
		Middle:      make([]byte, 12), // three zero counts: redirects name map, localised packages, package redirects
	}
	// Layout before the trailer: 16 + 4 + 4 + 4 + 4 + 12 = 44; trailer is 20, so the reserved soft-reference
	// window is the four bytes at 44..48 and the flag follows.
	h.SoftRefOffset = 60
	h.SoftRefSize = 4
	return h
}
