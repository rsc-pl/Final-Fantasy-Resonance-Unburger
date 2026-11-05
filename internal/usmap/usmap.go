// Package usmap reads Unreal Engine mapping files (.usmap, format v1–v4).
//
// A mapping file is the cooked-name + reflection description that the engine keeps out of the
// packages: a name table, the enums, and one record per script struct listing its properties in
// serialisation order. That order is exactly what an unversioned property stream needs, so this
// package is the schema source for DataTable row parsing.
package usmap

import (
	"encoding/binary"
	"fmt"
	"os"
)

// Magic is the .usmap file signature.
const Magic = 0x30C4

// Property is one serialised field of a struct.
type Property struct {
	Index    int
	ArrayDim int
	Name     string
	Type     string
	// Struct names the row struct for StructProperty.
	Struct string
	// Enum names the enum for EnumProperty; EnumInner is its underlying scalar type.
	Enum      string
	EnumInner string
	// Inner is the element type of ArrayProperty, SetProperty and OptionalProperty.
	Inner *Property
	// Key and Value are the MapProperty types.
	Key   *Property
	Value *Property
}

// Struct is one script struct record.
type Struct struct {
	// Declared is the property count the record declares when it differs from the descriptors actually
	// stored, which happens for properties with ArrayDim > 1.
	Declared   int
	Name       string
	Super      string
	Properties []Property
}

// Map is a parsed mapping file.
type Map struct {
	Names   []string
	Enums   map[string]map[int64]string
	Structs map[string]*Struct
	// Version is the file format version, and NameCount/StructCount the sizes actually parsed.
	Version     int
	NameCount   int
	StructCount int
	// structOffset and structTotal locate the struct section for StrictStructWalk.
	structOffset int
	structTotal  int
}

// Load reads a .usmap file from disk.
func Load(path string) (*Map, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes a mapping file held in memory.
func Parse(data []byte) (*Map, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("usmap: %d bytes, too short for a header", len(data))
	}
	le := binary.LittleEndian
	if magic := le.Uint16(data); magic != Magic {
		return nil, fmt.Errorf("usmap: bad magic %#x, want %#x", magic, Magic)
	}
	m := &Map{Version: int(data[2]), Enums: map[string]map[int64]string{}, Structs: map[string]*Struct{}}
	if m.Version < 1 || m.Version > 4 {
		return nil, fmt.Errorf("usmap: unsupported version %d", m.Version)
	}
	pos := 3
	if m.Version >= 3 {
		if pos >= len(data) {
			return nil, fmt.Errorf("usmap: truncated header")
		}
		hasVersioning := data[pos] != 0
		pos++
		if hasVersioning {
			if pos+4 > len(data) {
				return nil, fmt.Errorf("usmap: truncated package version")
			}
			pos += 4
			if m.Version >= 4 {
				// Major, Minor, Patch (u16 each), Change (u32), then a length-prefixed branch string.
				if pos+10 > len(data) {
					return nil, fmt.Errorf("usmap: truncated engine version")
				}
				pos += 10
				if pos+4 > len(data) {
					return nil, fmt.Errorf("usmap: truncated branch length")
				}
				n := int(int32(le.Uint32(data[pos:])))
				pos += 4
				if n < 0 || pos+n > len(data) {
					return nil, fmt.Errorf("usmap: branch length %d out of range", n)
				}
				pos += n
			}
		}
	}
	if pos+16 > len(data) {
		return nil, fmt.Errorf("usmap: truncated name table header")
	}
	// Four u32 fields precede the names: an unknown word, the compressed size, the uncompressed
	// size of the name region, and the name count. Every mapping file in this toolchain has the two
	// sizes equal because the region is stored uncompressed.
	compressed := int(int32(le.Uint32(data[pos+4:])))
	uncompressed := int(int32(le.Uint32(data[pos+8:])))
	nameCount := int(int32(le.Uint32(data[pos+12:])))
	pos += 16
	if compressed != uncompressed {
		return nil, fmt.Errorf("usmap: compressed name region (%d != %d) is not supported", compressed, uncompressed)
	}
	if nameCount <= 0 || nameCount > 1<<24 {
		return nil, fmt.Errorf("usmap: implausible name count %d", nameCount)
	}
	names := make([]string, nameCount)
	for i := 0; i < nameCount; i++ {
		if pos+2 > len(data) {
			return nil, fmt.Errorf("usmap: name %d truncated", i)
		}
		n := int(le.Uint16(data[pos:]))
		pos += 2
		if pos+n > len(data) {
			return nil, fmt.Errorf("usmap: name %d length %d out of range", i, n)
		}
		names[i] = string(data[pos : pos+n])
		pos += n
	}
	m.Names, m.NameCount = names, len(names)

	// Enums: name index, u16 value count, then (int64 value, name index) per entry.
	if pos+4 > len(data) {
		return nil, fmt.Errorf("usmap: truncated enum count")
	}
	enumCount := int(int32(le.Uint32(data[pos:])))
	pos += 4
	if enumCount < 0 || enumCount > 1<<20 {
		return nil, fmt.Errorf("usmap: implausible enum count %d", enumCount)
	}
	for i := 0; i < enumCount; i++ {
		if pos+6 > len(data) {
			return nil, fmt.Errorf("usmap: enum %d truncated", i)
		}
		nameIdx := int(int32(le.Uint32(data[pos:])))
		count := int(le.Uint16(data[pos+4:]))
		pos += 6
		if nameIdx < 0 || nameIdx >= len(names) {
			return nil, fmt.Errorf("usmap: enum %d name index %d out of range", i, nameIdx)
		}
		values := make(map[int64]string, count)
		for v := 0; v < count; v++ {
			if pos+12 > len(data) {
				return nil, fmt.Errorf("usmap: enum %d value %d truncated", i, v)
			}
			val := int64(le.Uint64(data[pos:]))
			valName := int(int32(le.Uint32(data[pos+8:])))
			pos += 12
			if valName < 0 || valName >= len(names) {
				return nil, fmt.Errorf("usmap: enum %s value name %d out of range", names[nameIdx], valName)
			}
			values[val] = names[valName]
		}
		m.Enums[names[nameIdx]] = values
	}

	// Structs: one record per script struct. The walk is resynchronising: a bad candidate is
	// rejected and the reader advances one byte, so a construct outside the known type set cannot
	// desynchronise every later struct. A struct is only recorded when every property parses and
	// the property indices are the exact sequence 0..n-1.
	if pos+4 > len(data) {
		return nil, fmt.Errorf("usmap: truncated struct count")
	}
	structCount := int(int32(le.Uint32(data[pos:])))
	pos += 4
	if structCount < 0 || structCount > 1<<20 {
		return nil, fmt.Errorf("usmap: implausible struct count %d", structCount)
	}
	m.structOffset, m.structTotal = pos, structCount
	for parsed := 0; parsed < structCount && pos < len(data); {
		s, end, err := m.parseStruct(data, pos)
		if err != nil {
			pos++
			continue
		}
		if _, dup := m.Structs[s.Name]; !dup {
			m.Structs[s.Name] = s
			m.StructCount++
			parsed++
		} else {
			parsed++
		}
		pos = end
	}
	if len(m.Structs) == 0 {
		return nil, fmt.Errorf("usmap: no struct could be parsed")
	}
	return m, nil
}

// propertyTypes is the EPropertyType order used by the format, following CUE4Parse's
// EPropertyType: Map 24, Set 25, Enum 26, FieldPath 27, Optional 28. There is no Int8 entry in this
// list — that absence is exactly what shifts Map from 25 to 24 compared with the older ordering, and
// assuming the older one is what made property descriptors drift by a byte.
var propertyTypes = []string{
	"ByteProperty", "BoolProperty", "IntProperty", "FloatProperty", "ObjectProperty",
	"NameProperty", "DelegateProperty", "DoubleProperty", "ArrayProperty", "StructProperty",
	"StrProperty", "TextProperty", "InterfaceProperty", "MulticastDelegateProperty",
	"WeakObjectProperty", "LazyObjectProperty", "AssetObjectProperty", "SoftObjectProperty",
	"UInt64Property", "UInt32Property", "UInt16Property", "Int64Property", "Int32Property",
	"Int16Property", "MapProperty", "SetProperty", "EnumProperty", "FieldPathProperty",
	"OptionalProperty", "Utf8StrProperty", "AnsiStrProperty",
}

// maxProperties bounds a struct record; a larger count means the candidate is not a struct.
const maxProperties = 2000

// parseStruct reads one struct record at pos and returns it with the offset just past it.
func (m *Map) parseStruct(data []byte, pos int) (*Struct, int, error) {
	le := binary.LittleEndian
	if pos+12 > len(data) {
		return nil, 0, fmt.Errorf("truncated")
	}
	nameIdx := int(int32(le.Uint32(data[pos:])))
	superIdx := int(int32(le.Uint32(data[pos+4:])))
	count := int(le.Uint16(data[pos+8:]))
	serialized := int(le.Uint16(data[pos+10:]))
	if nameIdx < 0 || nameIdx >= len(m.Names) {
		return nil, 0, fmt.Errorf("name index %d", nameIdx)
	}
	if superIdx < -1 || superIdx >= len(m.Names) {
		return nil, 0, fmt.Errorf("super index %d", superIdx)
	}
	if count > maxProperties || serialized > maxProperties {
		return nil, 0, fmt.Errorf("property count %d", count)
	}
	s := &Struct{Name: m.Names[nameIdx]}
	if superIdx >= 0 {
		s.Super = m.Names[superIdx]
	}
	p := pos + 12
	// The file carries one descriptor per *serialized* property; a property with ArrayDim > 1 is a single
	// descriptor that stands for several indices, and CUE4Parse expands it in memory rather than on disk.
	// So the descriptor count is `serialized`, and the record's index field is authoritative: it skips
	// ahead by the array dimension of the previous descriptor instead of advancing by one.
	descriptors := serialized
	if descriptors == 0 && count > 0 {
		descriptors = count
	}
	prev := -1
	for i := 0; i < descriptors; i++ {
		prop, next, err := m.parseProperty(data, p)
		if err != nil {
			return nil, 0, err
		}
		if prop.Index <= prev {
			return nil, 0, fmt.Errorf("property index %d after %d", prop.Index, prev)
		}
		prev = prop.Index
		s.Properties = append(s.Properties, *prop)
		p = next
	}
	if descriptors != count {
		s.Declared = count
	}
	return s, p, nil
}

// parseProperty reads one property record: u16 index, u8 array dim, i32 name, then the type.
func (m *Map) parseProperty(data []byte, pos int) (*Property, int, error) {
	le := binary.LittleEndian
	if pos+7 > len(data) {
		return nil, 0, fmt.Errorf("truncated property")
	}
	prop := &Property{Index: int(le.Uint16(data[pos:])), ArrayDim: int(data[pos+2])}
	nameIdx := int(int32(le.Uint32(data[pos+3:])))
	if nameIdx < 0 || nameIdx >= len(m.Names) {
		return nil, 0, fmt.Errorf("property name index %d", nameIdx)
	}
	prop.Name = m.Names[nameIdx]
	typ, next, err := m.parseType(data, pos+7, 0)
	if err != nil {
		return nil, 0, err
	}
	prop.Type = typ.Type
	prop.Struct = typ.Struct
	prop.Enum = typ.Enum
	prop.EnumInner = typ.EnumInner
	prop.Inner = typ.Inner
	prop.Key = typ.Key
	prop.Value = typ.Value
	return prop, next, nil
}

// parseType reads a type descriptor: a type byte followed by whatever that type carries.
func (m *Map) parseType(data []byte, pos int, depth int) (*Property, int, error) {
	le := binary.LittleEndian
	if depth > 8 {
		return nil, 0, fmt.Errorf("type nesting too deep")
	}
	if pos >= len(data) {
		return nil, 0, fmt.Errorf("truncated type")
	}
	t := int(data[pos])
	pos++
	if t >= len(propertyTypes) {
		return nil, 0, fmt.Errorf("unknown property type %d", t)
	}
	p := &Property{Type: propertyTypes[t]}
	switch p.Type {
	case "EnumProperty":
		if pos+5 > len(data) {
			return nil, 0, fmt.Errorf("truncated enum type")
		}
		inner := int(data[pos])
		if inner >= len(propertyTypes) {
			return nil, 0, fmt.Errorf("unknown enum inner type %d", inner)
		}
		p.EnumInner = propertyTypes[inner]
		idx := int(int32(le.Uint32(data[pos+1:])))
		if idx < 0 || idx >= len(m.Names) {
			return nil, 0, fmt.Errorf("enum name index %d", idx)
		}
		p.Enum = m.Names[idx]
		pos += 5
	case "StructProperty":
		if pos+4 > len(data) {
			return nil, 0, fmt.Errorf("truncated struct type")
		}
		idx := int(int32(le.Uint32(data[pos:])))
		if idx < 0 || idx >= len(m.Names) {
			return nil, 0, fmt.Errorf("struct name index %d", idx)
		}
		p.Struct = m.Names[idx]
		pos += 4
	case "ArrayProperty", "SetProperty", "OptionalProperty":
		// All three carry one nested type descriptor; Optional is not opaque.

		inner, next, err := m.parseType(data, pos, depth+1)
		if err != nil {
			return nil, 0, err
		}
		p.Inner = inner
		pos = next
	case "MapProperty":
		key, next, err := m.parseType(data, pos, depth+1)
		if err != nil {
			return nil, 0, err
		}
		val, next2, err := m.parseType(data, next, depth+1)
		if err != nil {
			return nil, 0, err
		}
		p.Key, p.Value = key, val
		pos = next2
	}
	return p, pos, nil
}

// Find returns a struct by name, following no inheritance.
func (m *Map) Find(name string) (*Struct, bool) {
	s, ok := m.Structs[name]
	return s, ok
}

// Chain returns the struct and its ancestors, nearest first, stopping at a missing or repeated name.
func (m *Map) Chain(name string) []*Struct {
	var out []*Struct
	seen := map[string]bool{}
	for name != "" && !seen[name] {
		seen[name] = true
		s, ok := m.Structs[name]
		if !ok {
			break
		}
		out = append(out, s)
		name = s.Super
	}
	return out
}

// Flatten returns the serialised property list of a struct: ancestors first, then the struct itself,
// with the index of each property renumbered so callers can address fields by position.
func (m *Map) Flatten(name string) ([]Property, error) {
	chain := m.Chain(name)
	if len(chain) == 0 {
		return nil, fmt.Errorf("usmap: struct %q not in the map", name)
	}
	var out []Property
	for i := len(chain) - 1; i >= 0; i-- {
		for _, p := range chain[i].Properties {
			p.Index = len(out)
			out = append(out, p)
		}
	}
	return out, nil
}

// StrictStructWalk parses the struct section without resynchronising: it stops at the first record that
// does not decode and reports how many decoded before it. It exists so the reader can be verified
// against the whole section instead of quietly recovering a subset.
func (m *Map) StrictStructWalk(data []byte) (int, error) {
	pos, count := m.structOffset, 0
	for count < m.structTotal && pos < len(data) {
		_, end, err := m.parseStruct(data, pos)
		if err != nil {
			return count, fmt.Errorf("struct %d at %d: %w", count, pos, err)
		}
		count++
		pos = end
	}
	return count, nil
}
