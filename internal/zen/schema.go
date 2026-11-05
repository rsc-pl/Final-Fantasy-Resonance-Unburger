package zen

import (
	"encoding/binary"
	"fmt"
	"math"

	"unburger/internal/usmap"
)

// FieldPair is one entry of a decoded map value.
type FieldPair struct {
	Key   FieldValue
	Value FieldValue
}

// FieldValue is one decoded property value from a schema-driven row.
type FieldValue struct {
	Name  string
	Type  string
	Int   int64
	Float float64
	Bool  bool
	Str   string
	Text  *FText
	Name2 FName
	Name3 FName
	// Struct holds the nested fields when the property is a StructProperty; Items holds the elements
	// of an array or set and Pairs the entries of a map.
	Struct []FieldValue
	Items  []FieldValue
	Pairs  []FieldPair
	// Present is false when the row's zero mask omitted the property.
	Present bool
}

// SchemaRow is one DataTable row decoded with the USMAP schema: the row name, the property values in
// serialisation order, and the byte range the row occupied.
type SchemaRow struct {
	Name   FName
	Values []FieldValue
	Start  int
	End    int
}

// Field returns the value for a property name, or false when the property is unknown to the schema.
func (r SchemaRow) Field(name string) (FieldValue, bool) {
	for _, v := range r.Values {
		if v.Name == name {
			return v, true
		}
	}
	return FieldValue{}, false
}

// Text returns the FText of the named property, or nil.
func (r SchemaRow) Text(name string) *FText {
	if v, ok := r.Field(name); ok && v.Present {
		return v.Text
	}
	return nil
}

// FName is a cooked name reference: an index into the package name map plus a number suffix.
type FName struct {
	Index  uint32
	Number uint32
}

// String resolves the name against a package name map, appending the number suffix when non-zero.
func (n FName) String(names []string) string {
	if int(n.Index) >= len(names) {
		return fmt.Sprintf("<%d>", n.Index)
	}
	if n.Number == 0 {
		return names[n.Index]
	}
	return fmt.Sprintf("%s_%d", names[n.Index], n.Number)
}

// schemaReader walks a property stream with a fixed schema.
type schemaReader struct {
	b     []byte
	pos   int
	m     *usmap.Map
	path  string
	depth int
}

// ParseSchemaRows decodes every row of a DataTable payload using the struct's schema. Rows are read
// until the payload is consumed exactly; a short read or a left-over byte is a hard error, so a
// schema that does not match the build cannot silently produce plausible rows.
func ParseSchemaRows(payload []byte, dataOffset int, props []usmap.Property, m *usmap.Map, names []string) ([]SchemaRow, error) {
	var rows []SchemaRow
	pos := dataOffset
	for pos < len(payload) {
		start := pos
		if pos+8 > len(payload) {
			// Return what was decoded so a caller can see where the table stopped and what is left.
			return rows, fmt.Errorf("schema row: row name truncated at %d of %d", pos, len(payload))
		}
		name := FName{Index: binary.LittleEndian.Uint32(payload[pos:]), Number: binary.LittleEndian.Uint32(payload[pos+4:])}
		pos += 8
		h, err := ParseUnversionedHeader(payload[pos:])
		if err != nil {
			return rows, fmt.Errorf("schema row %q at %d: %w", name.String(names), start, err)
		}
		pos += h.Size
		// An absent mask means no property is zero, so every value is on disk; a present mask has
		// exactly one bit per value in the fragments.
		if len(h.ZeroMask) != 0 && len(h.ZeroMask) != len(props) {
			return rows, fmt.Errorf("schema row %q: zero mask has %d bits, schema has %d properties",
				name.String(names), len(h.ZeroMask), len(props))
		}
		row := SchemaRow{Name: name, Start: start, Values: make([]FieldValue, 0, len(props))}
		for i, p := range props {
			present := len(h.ZeroMask) == 0 || !h.ZeroMask[i]
			v := FieldValue{Name: p.Name, Type: p.Type, Present: present}
			if !v.Present {
				row.Values = append(row.Values, v)
				continue
			}
			r := &schemaReader{b: payload, pos: pos, m: m, path: name.String(names)}
			if err := r.readValue(p, &v); err != nil {
				return rows, fmt.Errorf("schema row %q field %s: %w", name.String(names), p.Name, err)
			}
			pos = r.pos
			row.Values = append(row.Values, v)
		}
		row.End = pos
		rows = append(rows, row)
	}
	return rows, nil
}

// readValue decodes one value of the given property type.
func (r *schemaReader) readValue(p usmap.Property, v *FieldValue) error {
	le := binary.LittleEndian
	need := func(n int) error {
		if r.pos+n > len(r.b) {
			return fmt.Errorf("%s at %d: need %d bytes, %d left", p.Type, r.pos, n, len(r.b)-r.pos)
		}
		return nil
	}
	switch p.Type {
	case "IntProperty", "Int32Property":
		if err := need(4); err != nil {
			return err
		}
		v.Int = int64(int32(le.Uint32(r.b[r.pos:])))
		r.pos += 4
	case "UInt32Property":
		if err := need(4); err != nil {
			return err
		}
		v.Int = int64(le.Uint32(r.b[r.pos:]))
		r.pos += 4
	case "Int16Property":
		if err := need(2); err != nil {
			return err
		}
		v.Int = int64(int16(le.Uint16(r.b[r.pos:])))
		r.pos += 2
	case "Int8Property":
		if err := need(1); err != nil {
			return err
		}
		v.Int = int64(int8(r.b[r.pos]))
		r.pos++
	case "UInt16Property":
		if err := need(2); err != nil {
			return err
		}
		v.Int = int64(le.Uint16(r.b[r.pos:]))
		r.pos += 2
	case "Int64Property", "UInt64Property":
		if err := need(8); err != nil {
			return err
		}
		v.Int = int64(le.Uint64(r.b[r.pos:]))
		r.pos += 8
	case "FloatProperty":
		if err := need(4); err != nil {
			return err
		}
		v.Float = float64(math.Float32frombits(le.Uint32(r.b[r.pos:])))
		r.pos += 4
	case "DoubleProperty":
		if err := need(8); err != nil {
			return err
		}
		v.Float = float64(math.Float64frombits(le.Uint64(r.b[r.pos:])))
		r.pos += 8
	case "BoolProperty", "ByteProperty":
		if err := need(1); err != nil {
			return err
		}
		v.Bool = r.b[r.pos] != 0
		v.Int = int64(r.b[r.pos])
		r.pos++
	case "EnumProperty":
		switch p.EnumInner {
		case "ByteProperty", "":
			if err := need(1); err != nil {
				return err
			}
			v.Int = int64(r.b[r.pos])
			r.pos++
		case "IntProperty", "Int32Property":
			if err := need(4); err != nil {
				return err
			}
			v.Int = int64(int32(le.Uint32(r.b[r.pos:])))
			r.pos += 4
		case "UInt16Property":
			if err := need(2); err != nil {
				return err
			}
			v.Int = int64(le.Uint16(r.b[r.pos:]))
			r.pos += 2
		default:
			return fmt.Errorf("enum inner type %q is not supported", p.EnumInner)
		}
	case "NameProperty":
		if err := need(8); err != nil {
			return err
		}
		v.Name2 = FName{Index: le.Uint32(r.b[r.pos:]), Number: le.Uint32(r.b[r.pos+4:])}
		r.pos += 8
	case "StrProperty", "Utf8StrProperty", "AnsiStrProperty":
		s, err := r.readFString()
		if err != nil {
			return err
		}
		v.Str = s
	case "TextProperty":
		p2 := r.pos
		t, err := ReadFText(r.b, &p2)
		if err != nil {
			return err
		}
		v.Text = t
		r.pos = p2
	case "ObjectProperty":
		if err := need(4); err != nil {
			return err
		}
		v.Int = int64(int32(le.Uint32(r.b[r.pos:])))
		r.pos += 4
	case "StructProperty":
		return r.readStruct(p, v)
	case "SoftObjectProperty", "SoftClassProperty", "WeakObjectProperty", "LazyObjectProperty":
		// FSoftObjectPath in UE5: FTopLevelAssetPath (package name FName + asset name FName) followed by
		// the sub-path string. Reading a single FName here shifts the sub-path by eight bytes.
		if err := need(16); err != nil {
			return err
		}
		v.Name2 = FName{Index: le.Uint32(r.b[r.pos:]), Number: le.Uint32(r.b[r.pos+4:])}
		v.Name3 = FName{Index: le.Uint32(r.b[r.pos+8:]), Number: le.Uint32(r.b[r.pos+12:])}
		r.pos += 16
		sub, err := r.readFString()
		if err != nil {
			return err
		}
		v.Str = sub
	case "ArrayProperty", "SetProperty":
		if p.Inner == nil {
			return fmt.Errorf("array without an element type")
		}
		return r.readArray(p, v)
	case "MapProperty":
		return r.readMap(p, v)
	default:
		return fmt.Errorf("property type %q is not supported by the row reader", p.Type)
	}
	return nil
}

// readStruct decodes a nested struct by its schema. In an unversioned property stream a struct value
// is itself a property block: it carries its own FUnversionedHeader, and the properties whose zero bits
// are set are not on disk. Reading the fields straight through instead — without the nested header —
// shifts every following property in the row.
func (r *schemaReader) readStruct(p usmap.Property, v *FieldValue) error {
	if r.depth > 8 {
		return fmt.Errorf("struct nesting too deep")
	}
	props, err := r.m.Flatten(p.Struct)
	if err != nil {
		return err
	}
	h, err := ParseUnversionedHeader(r.b[r.pos:])
	if err != nil {
		return fmt.Errorf("%s header: %w", p.Struct, err)
	}
	r.pos += h.Size
	// A struct can be cooked with fewer serialized properties than the mapping declares: the trailing
	// ones are not on disk at all, so the mask is shorter than the schema and the remainder is absent.
	// That is the only mismatch accepted here — a mask longer than the schema is still a hard error.
	if len(h.ZeroMask) > len(props) {
		return fmt.Errorf("%s: zero mask has %d bits, schema has only %d properties", p.Struct, len(h.ZeroMask), len(props))
	}
	serialized := len(props)
	if len(h.ZeroMask) > 0 {
		serialized = len(h.ZeroMask)
	}
	r.depth++
	defer func() { r.depth-- }()
	v.Struct = make([]FieldValue, 0, len(props))
	for i, q := range props {
		present := i < serialized && (len(h.ZeroMask) == 0 || !h.ZeroMask[i])
		fv := FieldValue{Name: q.Name, Type: q.Type, Present: present}
		if present {
			if err := r.readValue(q, &fv); err != nil {
				return fmt.Errorf("%s.%s: %w", p.Struct, q.Name, err)
			}
		}
		v.Struct = append(v.Struct, fv)
	}
	return nil
}

// readFString reads a length-prefixed string; a negative length selects UTF-16.
func (r *schemaReader) readFString() (string, error) {
	le := binary.LittleEndian
	if r.pos+4 > len(r.b) {
		return "", fmt.Errorf("FString length at %d truncated", r.pos)
	}
	n := int(int32(le.Uint32(r.b[r.pos:])))
	r.pos += 4
	if n == 0 {
		return "", nil
	}
	if n > 0 {
		if r.pos+n > len(r.b) {
			return "", fmt.Errorf("FString body %d bytes at %d truncated", n, r.pos)
		}
		s := string(r.b[r.pos : r.pos+n-1])
		r.pos += n
		return s, nil
	}
	chars := -n
	if r.pos+chars*2 > len(r.b) {
		return "", fmt.Errorf("UTF-16 FString body %d chars at %d truncated", chars, r.pos)
	}
	buf := make([]rune, 0, chars)
	for i := 0; i < chars-1; i++ {
		buf = append(buf, rune(le.Uint16(r.b[r.pos+i*2:])))
	}
	r.pos += chars * 2
	return string(buf), nil
}

// readArray decodes a TArray or TSet: a count followed by that many elements. Struct elements are
// serialised with the struct's own schema and without a per-element unversioned header.
func (r *schemaReader) readArray(p usmap.Property, v *FieldValue) error {
	le := binary.LittleEndian
	if r.pos+4 > len(r.b) {
		return fmt.Errorf("array count at %d truncated", r.pos)
	}
	n := int(int32(le.Uint32(r.b[r.pos:])))
	r.pos += 4
	if n < 0 || n > 1<<20 {
		return fmt.Errorf("array count %d at %d is implausible", n, r.pos-4)
	}
	if r.depth > 6 {
		return fmt.Errorf("array nesting too deep")
	}
	r.depth++
	defer func() { r.depth-- }()
	v.Items = make([]FieldValue, 0, n)
	for i := 0; i < n; i++ {
		item := FieldValue{Name: p.Inner.Type, Type: p.Inner.Type, Present: true}
		if p.Inner.Type == "StructProperty" {
			// An array of structs stores each element's fields back to back, without the per-instance
			// unversioned header a standalone struct property carries.
			if err := r.readStructBody(p.Inner.Struct, &item); err != nil {
				return fmt.Errorf("element %d of %d: %w", i, n, err)
			}
			v.Items = append(v.Items, item)
			continue
		}
		if err := r.readValue(*p.Inner, &item); err != nil {
			return fmt.Errorf("element %d of %d: %w", i, n, err)
		}
		v.Items = append(v.Items, item)
	}
	return nil
}

// readMap decodes a TMap: a count, then every key, then every value.
func (r *schemaReader) readMap(p usmap.Property, v *FieldValue) error {
	le := binary.LittleEndian
	if p.Key == nil || p.Value == nil {
		return fmt.Errorf("map without key or value type")
	}
	if r.pos+4 > len(r.b) {
		return fmt.Errorf("map count at %d truncated", r.pos)
	}
	n := int(int32(le.Uint32(r.b[r.pos:])))
	r.pos += 4
	if n < 0 || n > 1<<20 {
		return fmt.Errorf("map count %d at %d is implausible", n, r.pos-4)
	}
	if r.depth > 6 {
		return fmt.Errorf("map nesting too deep")
	}
	r.depth++
	defer func() { r.depth-- }()
	keys := make([]FieldValue, n)
	for i := 0; i < n; i++ {
		keys[i] = FieldValue{Name: p.Key.Type, Type: p.Key.Type, Present: true}
		if err := r.readValue(*p.Key, &keys[i]); err != nil {
			return fmt.Errorf("map key %d of %d: %w", i, n, err)
		}
	}
	vals := make([]FieldValue, n)
	for i := 0; i < n; i++ {
		vals[i] = FieldValue{Name: p.Value.Type, Type: p.Value.Type, Present: true}
		if err := r.readValue(*p.Value, &vals[i]); err != nil {
			return fmt.Errorf("map value %d of %d: %w", i, n, err)
		}
	}
	v.Pairs = make([]FieldPair, 0, n)
	for i := 0; i < n; i++ {
		v.Pairs = append(v.Pairs, FieldPair{Key: keys[i], Value: vals[i]})
	}
	return nil
}

// readStructBody decodes a struct's fields without a leading unversioned header.
func (r *schemaReader) readStructBody(name string, v *FieldValue) error {
	props, err := r.m.Flatten(name)
	if err != nil {
		return err
	}
	if r.depth > 8 {
		return fmt.Errorf("struct nesting too deep")
	}
	r.depth++
	defer func() { r.depth-- }()
	v.Struct = make([]FieldValue, 0, len(props))
	for _, q := range props {
		fv := FieldValue{Name: q.Name, Type: q.Type, Present: true}
		if err := r.readValue(q, &fv); err != nil {
			return fmt.Errorf("%s.%s: %w", name, q.Name, err)
		}
		v.Struct = append(v.Struct, fv)
	}
	return nil
}

// ReadValueAt decodes one property value at pos and returns the position just past it, for diagnostics
// and for callers that walk a property stream field by field.
func ReadValueAt(b []byte, pos int, p usmap.Property, m *usmap.Map, depth int) (int, error) {
	r := &schemaReader{b: b, pos: pos, m: m, depth: depth}
	var v FieldValue
	if err := r.readValue(p, &v); err != nil {
		return pos, err
	}
	return r.pos, nil
}
