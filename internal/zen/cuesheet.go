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

// CueSheetRow is one row of an event-voice cue-sheet table: the row key, the cue sheet it names, and the
// soft object path it points at. Nothing here is decoded as a speaker: the table names cue sheets, and the
// person behind a cue is not stated in it.
type CueSheetRow struct {
	Row          string `json:"row"`
	CueSheetName string `json:"cue_sheet_name"`
	CuePath      string `json:"cue_path,omitempty"`
}

// CueSheetTable is one DT_EventVoice_* table.
type CueSheetTable struct {
	Asset     string        `json:"asset"`
	RowStruct string        `json:"row_struct"`
	Rows      []CueSheetRow `json:"rows"`
}

// CueSheetRecord is what gets written to records/cue-sheets.json.
type CueSheetRecord struct {
	Source   string          `json:"source"`
	Tables   []CueSheetTable `json:"tables"`
	RowTotal int             `json:"row_total"`
	Notes    []string        `json:"notes"`
}

// CollectCueSheets decodes every DT_EventVoice_* table in the container, resolving each row struct through
// the ScriptObjects table of global.utoc — that table is what turns the row struct's script import into the
// name SOUND_CUE_SHEET_ASSET.
func CollectCueSheets(paksDir string, decode iostore.Decoder, m *usmap.Map) (CueSheetRecord, error) {
	main, err := iostore.Open(filepath.Join(paksDir, "FFRS-Switch.utoc"))
	if err != nil {
		return CueSheetRecord{}, err
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
	if scripts == nil {
		return CueSheetRecord{}, errNoScriptObjects
	}
	payloads := map[string][]byte{}
	prologues := map[string]*DataTablePrologue{}
	names := map[string][]string{}
	for _, f := range main.Files() {
		if !strings.HasSuffix(f.Path, ".uasset") {
			continue
		}
		asset := strings.TrimSuffix(filepath.Base(f.Path), ".uasset")
		if !IsCueSheetTable(asset) {
			continue
		}
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
		payloads[asset] = payload
		prologues[asset] = pro
		names[asset] = pkg.Names
	}
	return BuildCueSheetRecord(payloads, prologues, m, names)
}

// BuildCueSheetRecord turns the decoded event-voice tables into a record. Tables are taken as they are:
// a table that did not decode exactly is left out rather than repaired, and a row whose cue sheet name is
// empty is kept with an empty name so the gap stays visible.
func BuildCueSheetRecord(payloads map[string][]byte, prologues map[string]*DataTablePrologue, m *usmap.Map, names map[string][]string) (CueSheetRecord, error) {
	assets := make([]string, 0, len(payloads))
	for a := range payloads {
		assets = append(assets, a)
	}
	sort.Strings(assets)
	rec := CueSheetRecord{
		Source: "DT_EventVoice_* via ScriptObjects(global.utoc) -> row struct SOUND_CUE_SHEET_ASSET",
		Notes: []string{
			"the table names cue sheets; it does not name a speaker or an addressee",
			"a cue name carries scene, line and a numeric suffix, and the suffix is not treated as a character id",
		},
	}
	for _, asset := range assets {
		pro := prologues[asset]
		if pro == nil {
			continue
		}
		rows, err := ParseDataTableSchema(payloads[asset], pro, m, names[asset])
		if err != nil {
			continue
		}
		tbl := CueSheetTable{Asset: asset, RowStruct: StructLeaf(pro.RowStructName)}
		for _, r := range rows {
			row := CueSheetRow{Row: r.Name.String(names[asset])}
			for _, v := range r.Values {
				switch v.Name {
				case "CueSheetName":
					row.CueSheetName = v.Str
				case "pCueSheet":
					row.CuePath = v.Str
					if row.CuePath == "" && (v.Name2.Index != 0 || v.Name2.Number != 0) {
						row.CuePath = "fname(" + itoa(v.Name2.Index) + "," + itoa(v.Name2.Number) + ")"
					}
				}
			}
			tbl.Rows = append(tbl.Rows, row)
		}
		rec.RowTotal += len(tbl.Rows)
		rec.Tables = append(rec.Tables, tbl)
	}
	return rec, nil
}

// WriteCueSheets writes the record as indented JSON.
func WriteCueSheets(path string, rec CueSheetRecord) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// errNoScriptObjects is returned when global.utoc has no ScriptObjects chunk: without it a row struct that
// is a script import cannot be named, and the tables stay unread rather than guessed.
var errNoScriptObjects = errScriptsMissing("no ScriptObjects chunk in global.utoc")

type errScriptsMissing string

func (e errScriptsMissing) Error() string { return string(e) }

func itoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

// IsCueSheetTable reports whether an asset is one of the event-voice cue-sheet tables.
func IsCueSheetTable(asset string) bool {
	return strings.HasPrefix(asset, "DT_EventVoice_")
}
