// Package zen reads Unreal Engine 5 Zen packages stored in IoStore chunks.
//
// Layout follows retoc (zen.rs, name_map.rs, container_header.rs) and CUE4Parse:
//
//	FIoContainerHeader   u32 magic, u32 version, u64 container id, ...
//	FZenPackageSummary   version dependent, see PackageSummary
//	FZenPackageVersioningInfo (when has_versioning_info != 0)
//	cell import/export offsets  (UE5.6, container header version >= 5)
//	FNameMap             name batch
//	FPackageObjectIndex  import map (8 bytes each)
//	FExportMapEntry      export map (72 bytes each)
//	FExportBundleEntry   export bundle entries (8 bytes each)
//
// Nothing here assumes a fixed offset: every section is bounded by the next offset and
// every count is derived from section sizes.
package zen

import (
	"encoding/binary"
	"fmt"
)

// ContainerHeaderVersion mirrors EIoContainerHeaderVersion.
type ContainerHeaderVersion uint32

// Known container header versions.
const (
	HeaderInitial                   ContainerHeaderVersion = 0
	HeaderLocalizedPackages         ContainerHeaderVersion = 1
	HeaderOptionalSegmentPackages   ContainerHeaderVersion = 2
	HeaderNoExportInfo              ContainerHeaderVersion = 3
	HeaderSoftPackageReferences     ContainerHeaderVersion = 4
	HeaderSoftPackageReferencesOffs ContainerHeaderVersion = 5
)

func (v ContainerHeaderVersion) String() string {
	switch v {
	case HeaderInitial:
		return "Initial"
	case HeaderLocalizedPackages:
		return "LocalizedPackages"
	case HeaderOptionalSegmentPackages:
		return "OptionalSegmentPackages"
	case HeaderNoExportInfo:
		return "NoExportInfo"
	case HeaderSoftPackageReferences:
		return "SoftPackageReferences"
	case HeaderSoftPackageReferencesOffs:
		return "SoftPackageReferencesOffset"
	}
	return fmt.Sprintf("Unknown(%d)", uint32(v))
}

// ContainerMagic is the first four bytes of a modern container header chunk.
const ContainerMagic = 0x496F436E // "nCoI"

// ContainerHeader holds the fields needed to read packages.
type ContainerHeader struct {
	Version     ContainerHeaderVersion
	ContainerID uint64
}

// ParseContainerHeader reads FIoContainerHeader up to the point where the version and the
// container id are known. The remaining tables are not needed to read packages.
func ParseContainerHeader(b []byte) (*ContainerHeader, error) {
	if len(b) < 16 {
		return nil, fmt.Errorf("container header: %d bytes", len(b))
	}
	le := binary.LittleEndian
	sig := le.Uint32(b)
	if sig != ContainerMagic {
		// Legacy UE4 container: the first four bytes are part of the id.
		id := uint64(sig) | uint64(le.Uint32(b[4:]))<<32
		return &ContainerHeader{Version: HeaderInitial, ContainerID: id}, nil
	}
	h := &ContainerHeader{Version: ContainerHeaderVersion(le.Uint32(b[4:]))}
	h.ContainerID = le.Uint64(b[8:])
	return h, nil
}

// MappedName is an 8-byte FMappedName: a 30-bit index plus a 2-bit table type and a number.
type MappedName struct {
	IndexAndType uint32
	Number       uint32
}

// Index returns the entry index inside its name table.
func (m MappedName) Index() uint32 { return m.IndexAndType & 0x3FFFFFFF }

// Type returns 0 for the package table, 1 for the container table, 2 for the global table.
func (m MappedName) Type() uint32 { return m.IndexAndType >> 30 }

// NameMap is a decoded FNameMap.
type NameMap []string

// Get resolves a mapped name. The stored number is one greater than the printed suffix.
func (n NameMap) Get(m MappedName) (string, error) {
	if int(m.Index()) >= len(n) {
		return "", fmt.Errorf("name index %d outside map of %d", m.Index(), len(n))
	}
	base := n[m.Index()]
	if m.Number == 0 {
		return base, nil
	}
	return fmt.Sprintf("%s_%d", base, m.Number-1), nil
}

// ReadNameBatch decodes an FNameMap: count, byte size, hash version, hashes, i16 lengths and
// the name bytes (UTF-8 when the length is positive, UTF-16 when negative).
func ReadNameBatch(b []byte) (NameMap, int, error) {
	if len(b) < 16 {
		return nil, 0, fmt.Errorf("name batch: %d bytes", len(b))
	}
	le := binary.LittleEndian
	num := int(le.Uint32(b))
	pos := 8
	if num == 0 {
		return NameMap{}, pos, nil
	}
	pos += 8       // hash algorithm id (count and byte size are already skipped)
	pos += num * 8 // hashes
	if pos+num*2 > len(b) {
		return nil, 0, fmt.Errorf("name batch: truncated lengths")
	}
	lengths := make([]int16, num)
	for i := range lengths {
		lengths[i] = int16(binary.BigEndian.Uint16(b[pos:]))
		pos += 2
	}
	names := make(NameMap, num)
	for i, l := range lengths {
		if l >= 0 {
			if pos+int(l) > len(b) {
				return nil, 0, fmt.Errorf("name batch: string %d outside the buffer", i)
			}
			names[i] = string(b[pos : pos+int(l)])
			pos += int(l)
			continue
		}
		// Negative length stores a UTF-16 string as i16::MIN + code units.
		units := 32768 + int(l)
		if pos+units*2 > len(b) {
			return nil, 0, fmt.Errorf("name batch: wide string %d outside the buffer", i)
		}
		raw := b[pos : pos+units*2]
		pos += units * 2
		runes := make([]rune, 0, units)
		for j := 0; j+1 < len(raw); j += 2 {
			runes = append(runes, rune(binary.LittleEndian.Uint16(raw[j:])))
		}
		names[i] = string(runes)
	}
	return names, pos, nil
}

// PackageSummary is FZenPackageSummary for containers newer than version 0.
type PackageSummary struct {
	HasVersioningInfo             uint32
	HeaderSize                    uint32
	Name                          MappedName
	PackageFlags                  uint32
	CookedHeaderSize              uint32
	ImportedPublicExportHashesOff int32
	ImportMapOffset               int32
	ExportMapOffset               int32
	ExportBundleEntriesOffset     int32
	DependencyBundleHeadersOffset int32
	DependencyBundleEntriesOffset int32
	ImportedPackageNamesOffset    int32
	// UE5.6 only.
	CellImportMapOffset int32
	CellExportMapOffset int32

	VersioningInfoOffset int32
	NameMapOffset        int32
}

// ParsePackageSummary reads the summary for a container header version newer than Initial.
func ParsePackageSummary(b []byte, v ContainerHeaderVersion) (*PackageSummary, error) {
	if v <= HeaderInitial {
		return nil, fmt.Errorf("legacy packages are not supported")
	}
	if len(b) < 4+4+8+4+4+4+4+4+4 {
		return nil, fmt.Errorf("package: %d bytes is too short for a summary", len(b))
	}
	le := binary.LittleEndian
	p := 0
	read32 := func() uint32 { x := le.Uint32(b[p:]); p += 4; return x }
	readI32 := func() int32 { x := int32(le.Uint32(b[p:])); p += 4; return x }

	s := &PackageSummary{}
	s.HasVersioningInfo = read32()
	s.HeaderSize = read32()
	s.Name.IndexAndType = read32()
	s.Name.Number = read32()
	s.PackageFlags = read32()
	s.CookedHeaderSize = read32()
	s.ImportedPublicExportHashesOff = readI32()
	s.ImportMapOffset = readI32()
	s.ExportMapOffset = readI32()
	s.ExportBundleEntriesOffset = readI32()
	if v >= HeaderNoExportInfo {
		s.DependencyBundleHeadersOffset = readI32()
		s.DependencyBundleEntriesOffset = readI32()
		s.ImportedPackageNamesOffset = readI32()
	}
	s.VersioningInfoOffset = int32(p)
	return s, nil
}
