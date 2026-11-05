package zen

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"unburger/internal/usmap"
)

// StructLeaf turns a resolved row-struct description into the struct's own name: the resolver appends
// the outer object and a module prefix, and the mapping file knows structs by bare name.
func StructLeaf(desc string) string {
	head := desc
	if i := strings.IndexByte(head, ' '); i >= 0 {
		head = head[:i]
	}
	return strings.TrimPrefix(head, "script:")
}

// maxRowPreamble bounds the search for the first row after the property block.
const maxRowPreamble = 24

// rowStreamMarker is the four-byte word that closes the row stream of a dialogue table. It was
// measured on all 287 TalkEventRowData tables whose rows otherwise consume the payload to the last
// four bytes — the value is identical in every one of them (0x01BF8000). Nothing is accepted on a
// partial match: a table that ends on any other value is still refused, and the rows themselves must
// tile exactly up to the marker.
const rowStreamMarker = 0x01BF8000

// endsWithRowMarker reports whether the payload closes with the measured row-stream marker.
func endsWithRowMarker(payload []byte) bool {
	if len(payload) < 4 {
		return false
	}
	return binary.LittleEndian.Uint32(payload[len(payload)-4:]) == rowStreamMarker
}

// SchemaTableResult is the outcome of decoding one DataTable with the mapping-file schema.
type SchemaTableResult struct {
	Asset      string
	RowStruct  string
	Properties int
	Rows       int
	// Consumed is the number of payload bytes the rows covered and Trailer the bytes of the closing
	// row-stream marker that follow them; Payload is the whole payload.
	Consumed int
	Trailer  int
	Payload  int
	// Err is the first reason the table could not be decoded exactly, or nil when it tiled.
	Err error
}

// Tiled reports whether every payload byte after the row offset was accounted for by decoded rows.
func (r SchemaTableResult) Tiled() bool {
	return r.Err == nil && r.Consumed+r.Trailer == r.Payload
}

// ParseDataTableSchema decodes a DataTable payload with the struct schema taken from the mapping file.
// It requires the rows to consume the payload exactly: a schema that does not match the build shows up
// as an error rather than as plausible-looking rows.
func ParseDataTableSchema(payload []byte, pro *DataTablePrologue, m *usmap.Map, names []string) ([]SchemaRow, error) {
	name := StructLeaf(pro.RowStructName)
	if name == "" {
		return nil, fmt.Errorf("row struct is unresolved")
	}
	return ParseDataTableSchemaFor(payload, pro, name, m, names)
}

// ParseDataTableSchemaFor is ParseDataTableSchema with the row-struct name given explicitly, for
// callers that resolve the struct reference themselves.
func ParseDataTableSchemaFor(payload []byte, pro *DataTablePrologue, structName string, m *usmap.Map, names []string) ([]SchemaRow, error) {
	name := structName
	props, err := m.Flatten(name)
	if err != nil {
		return nil, err
	}
	if pro.DataOffset <= 0 {
		return nil, fmt.Errorf("no data offset")
	}
	// The bytes between the property block and the first row are not always the same width, so the
	// row region is located by requiring whole rows to consume the payload exactly. Nothing else is
	// adjusted: the schema itself is fixed by the mapping file.
	var firstErr error
	for extra := 0; extra <= maxRowPreamble; extra++ {
		start := pro.DataOffset + extra
		if start >= len(payload) {
			break
		}
		rows, err := ParseSchemaRows(payload, start, props, m, names)
		if len(rows) > 0 {
			end := rows[len(rows)-1].End
			if err == nil && end == len(payload) {
				return rows, nil
			}
			// A row stream that stops exactly four bytes early and is closed by the measured marker
			// is accepted; the marker is required, not skipped.
			if end == len(payload)-4 && endsWithRowMarker(payload) {
				return rows, nil
			}
		}
		if firstErr == nil && err != nil {
			firstErr = fmt.Errorf("row preamble %d: %w", extra, err)
		}
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("no row preamble in 0..%d tiles the payload", maxRowPreamble)
	}
	return nil, firstErr
}

// SummariseSchemaResults groups results by row struct so a caller can see which structs the mapping
// file describes correctly for this build and which it does not.
func SummariseSchemaResults(results []SchemaTableResult) []string {
	type agg struct {
		structName string
		tables     int
		tiled      int
		rows       int
		reasons    map[string]int
	}
	byStruct := map[string]*agg{}
	for _, r := range results {
		a, ok := byStruct[r.RowStruct]
		if !ok {
			a = &agg{structName: r.RowStruct, reasons: map[string]int{}}
			byStruct[r.RowStruct] = a
		}
		a.tables++
		if r.Tiled() {
			a.tiled++
			a.rows += r.Rows
		} else if r.Err != nil {
			a.reasons[r.Err.Error()]++
		}
	}
	var keys []string
	for k := range byStruct {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return byStruct[keys[i]].tables > byStruct[keys[j]].tables
	})
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		a := byStruct[k]
		reason := ""
		if len(a.reasons) > 0 {
			var rs []string
			for r, n := range a.reasons {
				rs = append(rs, fmt.Sprintf("%dx %s", n, r))
			}
			sort.Strings(rs)
			reason = " | " + rs[0]
		}
		out = append(out, fmt.Sprintf("%-34s tables=%-4d tiled=%-4d rows=%-5d%s", k, a.tables, a.tiled, a.rows, reason))
	}
	return out
}

// WriteSchemaResults writes the per-table outcomes as JSON so failure causes can be classified
// without re-running the sweep.
func WriteSchemaResults(path string, results []SchemaTableResult) error {
	type row struct {
		Asset     string `json:"asset"`
		RowStruct string `json:"row_struct"`
		Rows      int    `json:"rows"`
		Payload   int    `json:"payload"`
		Consumed  int    `json:"consumed"`
		Tiled     bool   `json:"tiled"`
		Reason    string `json:"reason,omitempty"`
	}
	out := make([]row, 0, len(results))
	for _, r := range results {
		e := row{Asset: r.Asset, RowStruct: r.RowStruct, Rows: r.Rows, Payload: r.Payload, Consumed: r.Consumed, Tiled: r.Tiled()}
		if r.Err != nil {
			e.Reason = r.Err.Error()
		}
		out = append(out, e)
	}
	data, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
