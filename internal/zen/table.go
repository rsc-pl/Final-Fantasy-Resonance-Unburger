package zen

import (
	"encoding/binary"
	"fmt"
)

// PackageIndex is the 32-bit FPackageIndex used for object references in cooked property data:
//
//	0            null
//	> 0          export, entry (index - 1)
//	< 0          import, entry (-index - 1)
//
// It is four bytes, not eight. The eight bytes after a one-property header in a DataTable are
// therefore a PackageIndex plus four further bytes, not a single FPackageObjectIndex.
type PackageIndex int32

// IsNull reports the null reference.
func (p PackageIndex) IsNull() bool { return p == 0 }

// ExportIndex returns the export entry number.
func (p PackageIndex) ExportIndex() int { return int(p) - 1 }

// ImportIndex returns the import entry number.
func (p PackageIndex) ImportIndex() int { return int(-int64(p)) - 1 }

// ReadPackageIndex reads one FPackageIndex.
func ReadPackageIndex(b []byte, pos *int) (PackageIndex, error) {
	if *pos+4 > len(b) {
		return 0, fmt.Errorf("package index outside the payload")
	}
	v := PackageIndex(int32(binary.LittleEndian.Uint32(b[*pos:])))
	*pos += 4
	return v, nil
}

// DataTablePrologue is the part of a cooked UDataTable export that is provable without the row
// struct schema: the unversioned property header and the RowStruct reference that follows it.
type DataTablePrologue struct {
	Header    *UnversionedHeader
	RowStruct PackageIndex
	// RowStructName is resolved when the index addresses an import with a known script name.
	RowStructName string
	// DataOffset is where the table data starts, i.e. right after the serialized properties.
	DataOffset int
	// RowOffset is where the first row starts: DataOffset plus the eight-byte preamble the cooked
	// tables carry. Zero when the payload is too short to hold the preamble.
	RowOffset int
}

// ParseDataTablePrologue reads the property header and the RowStruct reference of a cooked
// UDataTable. It refuses anything it cannot prove: a header declaring more than one serialized
// property, a truncated value, or a RowStruct index that addresses nothing.
func ParseDataTablePrologue(b []byte, resolve func(PackageIndex) (string, bool)) (*DataTablePrologue, error) {
	h, err := ParseUnversionedHeader(b)
	if err != nil {
		return nil, err
	}
	if h.Values == 0 {
		// An empty property block: the row struct is not serialized on the object, so the table data
		// starts right after the header. Nothing is assumed about a struct.
		return &DataTablePrologue{Header: h, DataOffset: h.Size}, nil
	}
	if h.Values != 1 {
		return nil, fmt.Errorf("data table: %d serialized properties, expected 1 (RowStruct)", h.Values)
	}
	pos := h.Size
	rs, err := ReadPackageIndex(b, &pos)
	if err != nil {
		return nil, err
	}
	p := &DataTablePrologue{Header: h, RowStruct: rs, DataOffset: pos}
	if !rs.IsNull() {
		if rs >= 0 {
			return nil, fmt.Errorf("data table: RowStruct index %d addresses an export, not an import", rs)
		}
		if resolve != nil {
			if name, ok := resolve(rs); ok {
				p.RowStructName = name
			}
		}
	}
	// Between the RowStruct property and the first row every cooked DataTable in this build carries
	// exactly eight bytes (two u32 words). Measured on the Gilgamesh scenes, the arena tables, the
	// boss table and the ActiveTimeChat tables; the row walk then has to consume the rest of the
	// payload exactly, so a table that does not have this preamble fails loudly instead of decoding
	// shifted rows. Its meaning is still unknown, so it is skipped, not interpreted.
	if pos+8 <= len(b) {
		p.RowOffset = pos + 8
	}
	return p, nil
}

// ImportAt returns the resolved description of import entry i.
func (p *Package) ImportAt(i int, r *Resolver) (string, bool) {
	if i < 0 || i >= len(p.Imports) {
		return "", false
	}
	if r == nil {
		return fmt.Sprintf("import#%d", i), true
	}
	return r.Resolve(p.Imports[i]), true
}

// ResolvePackageIndex names the target of an index when it addresses this package's import map.
func (p *Package) ResolvePackageIndex(pi PackageIndex, r *Resolver) (string, bool) {
	if pi.IsNull() {
		return "null", true
	}
	if pi < 0 {
		return p.ImportAt(pi.ImportIndex(), r)
	}
	if i := pi.ExportIndex(); i >= 0 && i < len(p.Exports) {
		return "export:" + p.Named(p.Exports[i].ObjectName), true
	}
	return "", false
}
