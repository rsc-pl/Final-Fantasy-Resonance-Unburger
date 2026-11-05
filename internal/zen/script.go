package zen

import (
	"encoding/binary"
	"fmt"
)

// ScriptObjectEntry is an FScriptObjectEntry: 8-byte name plus three 64-bit indices.
type ScriptObjectEntry struct {
	ObjectName    MappedName
	GlobalIndex   uint64
	OuterIndex    uint64
	CDOClassIndex uint64
}

// ScriptObjects is the global script object table from a global container.
type ScriptObjects struct {
	GlobalNames NameMap
	Entries     []ScriptObjectEntry

	byGlobal map[uint64]int
}

// ParseScriptObjects decodes a ScriptObjects chunk: a global name map followed by the entries.
func ParseScriptObjects(b []byte) (*ScriptObjects, error) {
	names, used, err := ReadNameBatch(b)
	if err != nil {
		return nil, err
	}
	pos := used
	le := binary.LittleEndian
	if pos+4 > len(b) {
		return nil, fmt.Errorf("script objects: missing entry count")
	}
	count := int(le.Uint32(b[pos:]))
	pos += 4
	if count <= 0 || count > 1<<22 || pos+count*32 > len(b) {
		return nil, fmt.Errorf("script objects: implausible entry count %d", count)
	}
	s := &ScriptObjects{GlobalNames: names, Entries: make([]ScriptObjectEntry, count)}
	s.byGlobal = make(map[uint64]int, count)
	for i := range s.Entries {
		e := b[pos : pos+32]
		s.Entries[i] = ScriptObjectEntry{
			ObjectName:    MappedName{IndexAndType: le.Uint32(e[0:]), Number: le.Uint32(e[4:])},
			GlobalIndex:   le.Uint64(e[8:]),
			OuterIndex:    le.Uint64(e[16:]),
			CDOClassIndex: le.Uint64(e[24:]),
		}
		if _, dup := s.byGlobal[s.Entries[i].GlobalIndex]; dup {
			return nil, fmt.Errorf("script objects: duplicate global index %#x at entry %d", s.Entries[i].GlobalIndex, i)
		}
		if _, err := names.Get(s.Entries[i].ObjectName); err != nil {
			return nil, fmt.Errorf("script objects: entry %d name: %w", i, err)
		}
		s.byGlobal[s.Entries[i].GlobalIndex] = i
		pos += 32
	}
	if err := s.validateOuters(); err != nil {
		return nil, err
	}
	return s, nil
}

// validateOuters requires every outer index to be null or present, and every outer chain to
// terminate, so a corrupt table cannot make a lookup loop.
func (s *ScriptObjects) validateOuters() error {
	const nullIndex = ^uint64(0)
	for i := range s.Entries {
		outer := s.Entries[i].OuterIndex
		steps := 0
		for outer != nullIndex {
			j, ok := s.byGlobal[outer]
			if !ok {
				return fmt.Errorf("script objects: entry %d outer %#x is not in the table", i, outer)
			}
			outer = s.Entries[j].OuterIndex
			steps++
			if steps > len(s.Entries) {
				return fmt.Errorf("script objects: outer chain from entry %d does not terminate", i)
			}
		}
	}
	return nil
}

// Lookup resolves a script import index to a name.
func (s *ScriptObjects) Lookup(raw uint64) (string, ObjectIndex, bool) {
	i, ok := s.byGlobal[raw]
	if !ok {
		return "", 0, false
	}
	e := s.Entries[i]
	name, err := s.GlobalNames.Get(e.ObjectName)
	if err != nil {
		return "", 0, false
	}
	return name, ObjectIndex(e.OuterIndex), true
}

// Resolver resolves package object indices across a package and, when available, the global
// script object table.
type Resolver struct {
	Pkg     *Package
	Scripts *ScriptObjects
}

// Resolve returns a stable description of an index: the export name, the script object name,
// or an explicit unresolved marker. It never invents a class name.
func (r *Resolver) Resolve(o ObjectIndex) string {
	if o.IsNull() {
		return "null"
	}
	switch o.Kind() {
	case IndexExport:
		if int(o.Value()) < len(r.Pkg.Exports) {
			return "export:" + r.Pkg.Named(r.Pkg.Exports[o.Value()].ObjectName)
		}
		return fmt.Sprintf("export#%d(out of range)", o.Value())
	case IndexScriptImport:
		if r.Scripts != nil {
			if name, outer, ok := r.Scripts.Lookup(uint64(o)); ok {
				desc := "script:" + name
				if !ObjectIndex(outer).IsNull() {
					if outerName, _, ok := r.Scripts.Lookup(uint64(outer)); ok {
						desc += " outer:script:" + outerName
					}
				}
				return desc
			}
		}
		return fmt.Sprintf("script-import#%d(unresolved)", o.Value())
	case IndexPackageImport:
		return fmt.Sprintf("package-import#%d(unresolved)", o.Value())
	}
	return "null"
}

// ClassName resolves the class of an export when the script objects are available.
func (r *Resolver) ClassName(e Export) (string, bool) {
	if e.ClassIndex.Kind() != IndexScriptImport || r.Scripts == nil {
		return "", false
	}
	name, _, ok := r.Scripts.Lookup(uint64(e.ClassIndex))
	return name, ok
}
