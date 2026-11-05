package zen

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// ObjectIndex is an FPackageObjectIndex: a 64-bit value whose top two bits encode the kind.
type ObjectIndex uint64

// Object index kinds, from the top two bits.
const (
	IndexExport        = 0
	IndexScriptImport  = 1
	IndexPackageImport = 2
	IndexNull          = 3
)

// Kind returns the index kind.
func (o ObjectIndex) Kind() int { return int(uint64(o) >> 62) }

// Value returns the 62-bit payload.
func (o ObjectIndex) Value() uint64 { return uint64(o) & (1<<62 - 1) }

// IsNull reports the all-ones sentinel.
func (o ObjectIndex) IsNull() bool { return uint64(o) == ^uint64(0) }

// Export is an FExportMapEntry (72 bytes).
type Export struct {
	CookedSerialOffset uint64
	CookedSerialSize   uint64
	ObjectName         MappedName
	OuterIndex         ObjectIndex
	ClassIndex         ObjectIndex
	SuperIndex         ObjectIndex
	TemplateIndex      ObjectIndex
	PublicExportHash   uint64
	ObjectFlags        uint32
	FilterFlags        uint8
}

// BundleEntry is an FExportBundleEntry (8 bytes).
type BundleEntry struct {
	LocalExportIndex uint32
	CommandType      uint32
}

// Bundle command types.
const (
	CommandCreate    = 0
	CommandSerialize = 1
)

// Package is a parsed Zen package.
type Package struct {
	Version ContainerHeaderVersion

	Summary *PackageSummary
	Names   NameMap

	NameMapEnd     int
	CellImportMap  int32
	CellExportMap  int32
	HasCellOffsets bool
	VersioningInfo bool
	ZenPackageVer  uint32
	NameMapOffset  int

	Imports []ObjectIndex
	Exports []Export
	Bundle  []BundleEntry
}

// ParsePackage reads the summary, the name map and the export tables of a Zen package.
// The name map position is confirmed by requiring summary.Name to resolve to the expected
// package name, so an unsupported layout is reported instead of silently misparsed.
func ParsePackage(b []byte, v ContainerHeaderVersion, expectName string) (*Package, error) {
	s, err := ParsePackageSummary(b, v)
	if err != nil {
		return nil, err
	}
	p := &Package{Version: v, Summary: s}

	pos := int(s.VersioningInfoOffset)
	if s.HasVersioningInfo != 0 {
		next, zenVer, err := skipVersioningInfo(b, pos)
		if err != nil {
			return nil, err
		}
		p.VersioningInfo = true
		p.ZenPackageVer = zenVer
		pos = next
	}

	type candidate struct {
		names    NameMap
		start    int
		end      int
		cellImp  int32
		cellExp  int32
		hasCells bool
	}
	var candidates []candidate
	tryParse := func(start int, cellImp, cellExp int32, hasCells bool) (candidate, error) {
		names, used, err := ReadNameBatch(b[start:])
		if err != nil {
			return candidate{}, err
		}
		return candidate{names: names, start: start, end: start + used, cellImp: cellImp, cellExp: cellExp, hasCells: hasCells}, nil
	}

	if v >= HeaderSoftPackageReferencesOffs && pos+8 <= len(b) {
		le := binary.LittleEndian
		ci := int32(le.Uint32(b[pos:]))
		ce := int32(le.Uint32(b[pos+4:]))
		if c, err := tryParse(pos+8, ci, ce, true); err == nil {
			candidates = append(candidates, c)
		}
	}
	if c, err := tryParse(pos, -1, -1, false); err == nil {
		candidates = append(candidates, c)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no name map layout validated at offset %d", pos)
	}

	var chosen *candidate
	for i := range candidates {
		c := &candidates[i]
		name, err := c.names.Get(s.Name)
		if err != nil {
			continue
		}
		if expectName != "" && !nameMatches(name, expectName) {
			continue
		}
		// The name map must fit before the first table; exact equality is not required
		// because padding may follow it.
		limit := firstTableOffset(s)
		if limit > 0 && c.end > limit {
			continue
		}
		chosen = c
		break
	}
	if chosen == nil {
		name, _ := candidates[0].names.Get(s.Name)
		return nil, fmt.Errorf("name map does not resolve package name %q (got %q)", expectName, name)
	}
	p.Names = chosen.names
	p.NameMapOffset = chosen.start
	p.NameMapEnd = chosen.end
	p.CellImportMap = chosen.cellImp
	p.CellExportMap = chosen.cellExp
	p.HasCellOffsets = chosen.hasCells

	if err := p.readTables(b); err != nil {
		return nil, err
	}
	return p, nil
}

func firstTableOffset(s *PackageSummary) int {
	best := 0
	for _, v := range []int32{s.ImportedPublicExportHashesOff, s.ImportMapOffset, s.ExportMapOffset, s.ExportBundleEntriesOffset} {
		if v > 0 && (best == 0 || int(v) < best) {
			best = int(v)
		}
	}
	return best
}

// nameMatches accepts a full package path or a bare asset name: the resolved mapped name is
// the package path, so the comparison uses its last segment.
func nameMatches(resolved, expect string) bool {
	last := resolved
	if i := strings.LastIndexByte(last, '/'); i >= 0 {
		last = last[i+1:]
	}
	return last == expect || strings.HasSuffix(last, "/"+expect)
}

func skipVersioningInfo(b []byte, pos int) (int, uint32, error) {
	le := binary.LittleEndian
	if pos+20 > len(b) {
		return 0, 0, fmt.Errorf("versioning info outside the package")
	}
	zenVer := le.Uint32(b[pos:])
	pos += 4 // EZenPackageVersion
	pos += 4 // file version UE4
	pos += 4 // file version UE5
	pos += 4 // licensee version
	count := int(le.Uint32(b[pos:]))
	pos += 4
	if count < 0 || count > 1<<16 {
		return 0, 0, fmt.Errorf("implausible custom version count %d", count)
	}
	pos += count * 20 // FCustomVersion: 16-byte key + 4-byte version
	if pos > len(b) {
		return 0, 0, fmt.Errorf("versioning info outside the package")
	}
	return pos, zenVer, nil
}

func (p *Package) readTables(b []byte) error {
	s := p.Summary
	le := binary.LittleEndian

	importOff := int(s.ImportMapOffset)
	exportOff := int(s.ExportMapOffset)
	bundleOff := int(s.ExportBundleEntriesOffset)
	if importOff < 0 || exportOff < importOff || bundleOff < exportOff || bundleOff > len(b) {
		return fmt.Errorf("implausible section offsets: import=%d export=%d bundle=%d", importOff, exportOff, bundleOff)
	}

	importEnd := exportOff
	exportEnd := bundleOff
	if p.HasCellOffsets {
		exportEnd = int(p.CellImportMap)
		if exportEnd < exportOff || int(p.CellExportMap) < exportEnd || bundleOff < int(p.CellExportMap) {
			return fmt.Errorf("implausible cell offsets: %d %d", p.CellImportMap, p.CellExportMap)
		}
		if (exportEnd-exportOff)%72 != 0 {
			return fmt.Errorf("export map is not a whole number of 72-byte entries")
		}
		if (int(p.CellExportMap)-exportEnd)%8 != 0 {
			return fmt.Errorf("cell import map is not a whole number of 8-byte entries")
		}
		if (bundleOff-int(p.CellExportMap))%40 != 0 {
			return fmt.Errorf("cell export map is not a whole number of 40-byte entries")
		}
	}
	if (importEnd-importOff)%8 != 0 {
		return fmt.Errorf("import map is not a whole number of 8-byte entries")
	}
	if (exportEnd-exportOff)%72 != 0 {
		return fmt.Errorf("export map is not a whole number of 72-byte entries")
	}

	nImports := (importEnd - importOff) / 8
	p.Imports = make([]ObjectIndex, nImports)
	for i := range p.Imports {
		p.Imports[i] = ObjectIndex(le.Uint64(b[importOff+i*8:]))
	}

	nExports := (exportEnd - exportOff) / 72
	p.Exports = make([]Export, nExports)
	for i := range p.Exports {
		e := b[exportOff+i*72:]
		p.Exports[i] = Export{
			CookedSerialOffset: le.Uint64(e[0:]),
			CookedSerialSize:   le.Uint64(e[8:]),
			ObjectName:         MappedName{IndexAndType: le.Uint32(e[16:]), Number: le.Uint32(e[20:])},
			OuterIndex:         ObjectIndex(le.Uint64(e[24:])),
			ClassIndex:         ObjectIndex(le.Uint64(e[32:])),
			SuperIndex:         ObjectIndex(le.Uint64(e[40:])),
			TemplateIndex:      ObjectIndex(le.Uint64(e[48:])),
			PublicExportHash:   le.Uint64(e[56:]),
			ObjectFlags:        le.Uint32(e[64:]),
			FilterFlags:        e[68],
		}
	}

	// The bundle covers both the classic and the cell exports, so its end is the next
	// section: dependency bundle headers, or the cooked header end.
	bundleEnd := int(s.DependencyBundleHeadersOffset)
	if bundleEnd <= 0 || bundleEnd > len(b) {
		bundleEnd = int(s.HeaderSize)
	}
	if bundleEnd < bundleOff || bundleEnd > len(b) {
		return fmt.Errorf("implausible export bundle end %d", bundleEnd)
	}
	if (bundleEnd-bundleOff)%8 != 0 {
		return fmt.Errorf("export bundle is not a whole number of 8-byte entries")
	}
	n := (bundleEnd - bundleOff) / 8
	p.Bundle = make([]BundleEntry, n)
	for i := range p.Bundle {
		p.Bundle[i] = BundleEntry{
			LocalExportIndex: le.Uint32(b[bundleOff+i*8:]),
			CommandType:      le.Uint32(b[bundleOff+i*8+4:]),
		}
	}
	return nil
}

// Named returns the printable name of a mapped name.
func (p *Package) Named(m MappedName) string {
	s, err := p.Names.Get(m)
	if err != nil {
		return fmt.Sprintf("<unresolved %d/%d>", m.Index(), m.Type())
	}
	return s
}

// ExportClassName describes how an object index resolves, without inventing a name.
func (p *Package) DescribeIndex(o ObjectIndex) string {
	switch o.Kind() {
	case IndexExport:
		if o.IsNull() {
			return "null"
		}
		if int(o.Value()) < len(p.Exports) {
			return "export:" + p.Named(p.Exports[o.Value()].ObjectName)
		}
		return fmt.Sprintf("export#%d(out of range)", o.Value())
	case IndexScriptImport:
		return fmt.Sprintf("script-import#%d(unresolved)", o.Value())
	case IndexPackageImport:
		return fmt.Sprintf("package-import#%d(unresolved)", o.Value())
	}
	return "null"
}

// ExportPayload returns the serialized bytes of one export. Cooked serial offsets are
// relative to the end of the cooked header (summary.HeaderSize), not to the file start.
func (p *Package) ExportPayload(b []byte, i int) ([]byte, error) {
	if i < 0 || i >= len(p.Exports) {
		return nil, fmt.Errorf("export %d does not exist", i)
	}
	e := p.Exports[i]
	off := int64(p.Summary.HeaderSize) + int64(e.CookedSerialOffset)
	end := off + int64(e.CookedSerialSize)
	if off < 0 || end > int64(len(b)) || end < off {
		return nil, fmt.Errorf("export %d payload [%d,%d) outside the %d byte package", i, off, end, len(b))
	}
	return b[off:end], nil
}
