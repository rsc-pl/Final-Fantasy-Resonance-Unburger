package zen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"unburger/internal/iostore"
	"unburger/internal/usmap"
)

// DialogueRow is one DataTable row that carries a dialogue key, read with the mapping-file schema.
// The speaker is kept as the raw FName the table stores: it is a source label, not a character
// identity, and the two are never merged here.
type DialogueRow struct {
	Table   string `json:"table"`
	RowName string `json:"row_name"`
	// Key is the string-table key from ContentText, empty when the row has no text.
	Key string `json:"key,omitempty"`
	// Speaker is the raw speaker FName as stored in the row, empty when the row has none.
	Speaker string `json:"speaker,omitempty"`
	// SpeakerIndex and SpeakerNumber are the raw FName components, kept so a later pass can resolve
	// identity without re-reading the asset.
	SpeakerIndex  uint32 `json:"speaker_index"`
	SpeakerNumber uint32 `json:"speaker_number"`
	TalkID        int64  `json:"talk_id"`
	Voice         string `json:"voice,omitempty"`
	RowStruct     string `json:"row_struct"`
	TableFile     string `json:"table_file"`
}

// DialogueResult is the outcome of a schema-driven dialogue sweep.
type DialogueResult struct {
	Tables  int           `json:"tables"`
	Tiled   int           `json:"tiled"`
	Rows    int           `json:"rows"`
	WithKey int           `json:"rows_with_key"`
	Items   []DialogueRow `json:"items"`
	Failed  []SchemaTableResult
}

// CollectDialogueRows reads every DataTable in the container with the mapping-file schema and keeps
// the rows that carry a dialogue key or a speaker. Tables that do not tile exactly contribute
// nothing: a table whose rows cannot be accounted for byte by byte is reported, not harvested.
func CollectDialogueRows(paksDir string, decode iostore.Decoder, m *usmap.Map) (*DialogueResult, error) {
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
	res := &DialogueResult{}
	for _, f := range main.Files() {
		if !strings.HasSuffix(f.Path, ".uasset") || !strings.Contains(f.Path, "/Datatable/") {
			continue
		}
		asset := strings.TrimSuffix(filepath.Base(f.Path), ".uasset")
		data, err := main.ReadFile(f, decode)
		if err != nil {
			continue
		}
		pkg, err := ParsePackage(data, HeaderSoftPackageReferencesOffs, asset)
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
		structName := StructLeaf(pro.RowStructName)
		if structName != "TalkEventRowData" && structName != "ChatRowData" {
			continue
		}
		res.Tables++
		rows, err := ParseDataTableSchema(payload, pro, m, pkg.Names)
		if err != nil {
			res.Failed = append(res.Failed, SchemaTableResult{Asset: asset, RowStruct: structName, Payload: len(payload), Err: err})
			continue
		}
		res.Tiled++
		for _, row := range rows {
			res.Rows++
			item := DialogueRow{
				Table:     asset,
				RowName:   row.Name.String(pkg.Names),
				RowStruct: structName,
				TableFile: f.Path,
			}
			if ft := row.Text("ContentText"); ft != nil && ft.Key != "" {
				item.Key = ft.Key
				res.WithKey++
			}
			if v, ok := row.Field("Speaker"); ok && v.Present {
				item.Speaker = v.Name2.String(pkg.Names)
				item.SpeakerIndex = v.Name2.Index
				item.SpeakerNumber = v.Name2.Number
			}
			if v, ok := row.Field("TalkId"); ok && v.Present {
				item.TalkID = v.Int
			} else {
				item.TalkID = -1
			}
			if v, ok := row.Field("Voice"); ok {
				item.Voice = v.Str
			}
			if item.Key == "" && item.Speaker == "" {
				continue
			}
			res.Items = append(res.Items, item)
		}
	}
	sort.Slice(res.Items, func(i, j int) bool {
		if res.Items[i].Table != res.Items[j].Table {
			return res.Items[i].Table < res.Items[j].Table
		}
		return res.Items[i].Key < res.Items[j].Key
	})
	return res, nil
}

// WriteDialogueRows writes the sweep to JSON.
func WriteDialogueRows(path string, res *DialogueResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
