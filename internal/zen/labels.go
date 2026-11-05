package zen

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"os"

	"unburger/internal/iostore"
)

// RowLabel is one dialogue key with the speaker label that shares its row.
type RowLabel struct {
	Table    string `json:"table"`
	Struct   string `json:"row_struct"`
	Row      int    `json:"row"`
	Key      string `json:"key"`
	Label    string `json:"label"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	LabelOff int    `json:"label_offset"`
}

// TalkLabels walks every TalkEventRowData table in a container and collects key -> label for the
// rows whose boundaries parse. Tables whose rows cannot be proven are reported, never guessed.
type TalkLabels struct {
	Tables   int
	Parsed   int
	Rows     int
	ByStruct map[string]int
	Items    []RowLabel
	Failed   []string
}

// CollectTalkLabels performs the walk.
func CollectTalkLabels(paksDir string, decode DecodeFunc) (*TalkLabels, error) {
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
	out := &TalkLabels{ByStruct: map[string]int{}}
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
		out.Tables++
		structName := pro.RowStructName
		if strings.HasPrefix(structName, "script:") {
			structName = structName[len("script:"):]
			if i := strings.IndexByte(structName, ' '); i >= 0 {
				structName = structName[:i]
			}
		}
		out.ByStruct[structName] += 0
		textStruct := structName == "TalkEventRowData" || structName == "ChatRowData" || structName == "TalkEventData"
		unknownStruct := structName == ""
		rows, err := ParseTableRows(pkg, payload, pro.DataOffset)
		if err != nil {
			out.Failed = append(out.Failed, fmt.Sprintf("%s	%s	%s", f.Path, base, err))
			continue
		}
		out.Parsed++
		out.ByStruct[structName]++
		keyed := 0
		for _, row := range rows {
			if row.Key != "" {
				keyed++
			}
		}
		// A table without a serialized RowStruct is only treated as text when most of its rows carry
		// an FText key; that is what separates a dialogue table from an arbitrary one.
		if unknownStruct && keyed*2 >= len(rows) {
			textStruct = true
			structName = "unknown-text"
		}
		for _, row := range rows {
			out.Rows++
			if !textStruct || row.Key == "" || row.Label == "" {
				continue
			}
			out.Items = append(out.Items, RowLabel{
				Table: base, Struct: structName, Row: row.Index, Key: row.Key, Label: row.Label,
				Start: row.Start, End: row.End, LabelOff: row.LabelOff,
			})
		}
	}
	sort.Slice(out.Items, func(i, j int) bool {
		if out.Items[i].Table != out.Items[j].Table {
			return out.Items[i].Table < out.Items[j].Table
		}
		return out.Items[i].Row < out.Items[j].Row
	})
	return out, nil
}

// WriteLabels stores the collected pairs as JSON.
func WriteLabels(path string, t *TalkLabels) error {
	var b strings.Builder
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  \"tables\": %d,\n  \"parsed\": %d,\n  \"rows\": %d,\n  \"items\": [\n", t.Tables, t.Parsed, t.Rows)
	for i, it := range t.Items {
		fmt.Fprintf(&b, "    {\"table\":%q,\"row_struct\":%q,\"row\":%d,\"key\":%q,\"label\":%q,\"start\":%d,\"end\":%d,\"label_offset\":%d}",
			it.Table, it.Struct, it.Row, it.Key, it.Label, it.Start, it.End, it.LabelOff)
		if i+1 < len(t.Items) {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  ],\n  \"failed\": [\n")
	for i, f := range t.Failed {
		fmt.Fprintf(&b, "    %q", f)
		if i+1 < len(t.Failed) {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  ]\n}\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
