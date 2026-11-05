package zen

import (
	"path/filepath"
	"sort"
	"strings"

	"unburger/internal/iostore"
	"unburger/internal/usmap"
)

// CollectSchemaTables decodes every DataTable in the container with the mapping-file schema and
// records, per table, whether the rows consumed the payload exactly. It never rewrites a table: a
// schema that does not match the build is reported, not repaired.
func CollectSchemaTables(paksDir string, decode iostore.Decoder, m *usmap.Map) ([]SchemaTableResult, error) {
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
	var out []SchemaTableResult
	for _, f := range main.Files() {
		if !strings.HasSuffix(f.Path, ".uasset") || !strings.Contains(f.Path, "/Datatable/") {
			continue
		}
		asset := strings.TrimSuffix(filepath.Base(f.Path), ".uasset")
		data, err := main.ReadFile(f, decode)
		if err != nil {
			out = append(out, SchemaTableResult{Asset: asset, Err: err})
			continue
		}
		pkg, err := ParsePackage(data, HeaderSoftPackageReferencesOffs, asset)
		if err != nil || len(pkg.Exports) == 0 {
			out = append(out, SchemaTableResult{Asset: asset, Err: err})
			continue
		}
		payload, err := pkg.ExportPayload(data, 0)
		if err != nil {
			out = append(out, SchemaTableResult{Asset: asset, Err: err})
			continue
		}
		r := &Resolver{Pkg: pkg, Scripts: scripts}
		pro, err := ParseDataTablePrologue(payload, func(pi PackageIndex) (string, bool) {
			return pkg.ResolvePackageIndex(pi, r)
		})
		if err != nil {
			out = append(out, SchemaTableResult{Asset: asset, Payload: len(payload), Err: err})
			continue
		}
		res := SchemaTableResult{
			Asset:     asset,
			RowStruct: StructLeaf(pro.RowStructName),
			Payload:   len(payload),
		}
		rows, err := ParseDataTableSchema(payload, pro, m, pkg.Names)
		res.Rows = len(rows)
		if len(rows) > 0 {
			// End is measured from the start of the payload, so it is directly comparable with the
			// payload length; subtracting the row offset would understate it by the header.
			res.Consumed = rows[len(rows)-1].End
			if res.Consumed != len(payload) && len(payload)-res.Consumed == 4 && endsWithRowMarker(payload) {
				res.Trailer = 4
			}
		}
		if props, ferr := m.Flatten(res.RowStruct); ferr == nil {
			res.Properties = len(props)
		}
		res.Err = err
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Asset < out[j].Asset })
	return out, nil
}
