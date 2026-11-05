package zen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"unburger/internal/iostore"
)

// DecodeFunc expands one compressed block.
type DecodeFunc = iostore.Decoder

// TableInfo describes one DataTable package found in a container.
type TableInfo struct {
	Path        string `json:"path"`
	Package     string `json:"package"`
	RowStruct   string `json:"row_struct"`
	RowStructID int32  `json:"row_struct_index"`
	ExportSize  int    `json:"export_size"`
	DataOffset  int    `json:"data_offset"`
	Names       int    `json:"names"`
}

// Inventory walks every DataTable package in a container and records the row struct it uses.
// The row struct reference is proven per package, so the census is a set of verified facts even
// where the row contents cannot be decoded yet.
func Inventory(paksDir string, decode DecodeFunc) ([]TableInfo, error) {
	main, err := iostore.Open(filepath.Join(paksDir, "FFRS-Switch.utoc"))
	if err != nil {
		return nil, err
	}
	var scripts *ScriptObjects
	if g, err := iostore.Open(filepath.Join(paksDir, "global.utoc")); err == nil {
		for _, e := range g.Entries() {
			if e.ID.Type() != 5 {
				continue
			}
			if data, err := g.Read(e.ID, decode); err == nil {
				if s, err := ParseScriptObjects(data); err == nil {
					scripts = s
				}
			}
			break
		}
	}

	var out []TableInfo
	for _, f := range main.Files() {
		if !strings.HasSuffix(f.Path, ".uasset") || !strings.Contains(f.Path, "/Datatable/") {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(f.Path), ".uasset")
		data, err := main.ReadFile(f, decode)
		if err != nil {
			continue
		}
		pkg, err := ParsePackage(data, HeaderSoftPackageReferencesOffs, base)
		if err != nil || len(pkg.Exports) == 0 {
			continue
		}
		payload, err := pkg.ExportPayload(data, 0)
		if err != nil {
			continue
		}
		r := &Resolver{Pkg: pkg, Scripts: scripts}
		pro, err := ParseDataTablePrologue(payload, func(pi PackageIndex) (string, bool) {
			return pkg.ResolvePackageIndex(pi, r)
		})
		if err != nil {
			continue
		}
		name := pro.RowStructName
		if name == "" {
			name = fmt.Sprintf("<unresolved %d>", pro.RowStruct)
		}
		out = append(out, TableInfo{
			Path:        strings.TrimPrefix(f.Path, "../../../FFRS/Content/"),
			Package:     pkg.Named(pkg.Summary.Name),
			RowStruct:   name,
			RowStructID: int32(pro.RowStruct),
			ExportSize:  len(payload),
			DataOffset:  pro.DataOffset,
			Names:       len(pkg.Names),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// WriteInventory stores the census as JSON.
func WriteInventory(path string, tables []TableInfo) error {
	var b strings.Builder
	b.WriteString("[\n")
	for i, t := range tables {
		b.WriteString("  {")
		fmt.Fprintf(&b, "\"path\":%q,\"package\":%q,\"row_struct\":%q,\"row_struct_index\":%d,\"export_size\":%d,\"data_offset\":%d,\"names\":%d",
			t.Path, t.Package, t.RowStruct, t.RowStructID, t.ExportSize, t.DataOffset, t.Names)
		b.WriteString("}")
		if i+1 < len(tables) {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("]\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
