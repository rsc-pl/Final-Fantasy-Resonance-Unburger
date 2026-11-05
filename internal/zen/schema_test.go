package zen_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unburger/internal/usmap"
	"unburger/internal/zen"
)

// loadSchema loads the USMAP that ships with the project and flattens one struct's schema.
func loadSchema(t *testing.T, structName string) (*usmap.Map, []usmap.Property) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var root string
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			root = dir
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
	path := filepath.Join(root, ".local/toolchain/maps/FFRS-5.6.1-0+UE5-24b12662.usmap")
	if _, err := os.Stat(path); err != nil {
		t.Skip("no mapping file on this host")
	}
	m, err := usmap.Load(path)
	if err != nil {
		t.Fatalf("load usmap: %v", err)
	}
	props, err := m.Flatten(structName)
	if err != nil {
		t.Fatalf("flatten %s: %v", structName, err)
	}
	return m, props
}

// TestSchemaRowsGilgamesh01 decodes the scene with the USMAP schema and requires the bytes to be
// consumed exactly, the dialogue keys to run in order, and the speaker names to match what the
// heuristic walk found independently.
func TestSchemaRowsGilgamesh01(t *testing.T) {
	m, props := loadSchema(t, "TalkEventRowData")
	if len(props) != 10 {
		t.Fatalf("schema has %d properties, want 10", len(props))
	}
	names := []string{
		"TalkId", "WindowType", "Speaker", "Emotion", "WindowPosition",
		"ContentText", "Voice", "TextDisplayInterval", "IsLogginDisable", "IsVoiceValid",
	}
	for i, p := range props {
		if p.Name != names[i] {
			t.Fatalf("property %d is %q, want %q", i, p.Name, names[i])
		}
	}
	pkg, payload, pro := tablePayload(t, "DT_SubEv_C09_Gilgamesh_01")
	rows, err := zen.ParseSchemaRows(payload, pro.RowOffset, props, m, pkg.Names)
	if err != nil {
		t.Fatalf("schema rows: %v", err)
	}
	if len(rows) != 18 {
		t.Fatalf("decoded %d rows, want 18", len(rows))
	}
	if rows[len(rows)-1].End != len(payload) {
		t.Fatalf("rows end at %d, payload is %d bytes", rows[len(rows)-1].End, len(payload))
	}
	wantKeys := []string{"_001", "_002", "_003", "_004", "_005", "_006", "_007", "_008", "_009"}
	wantSpeakers := []string{"レイン", "ラスウェル", "レイン", "テロップ", "レイン", "テロップ", "ラスウェル", "テロップ", "テロップ"}
	var keys []string
	var speakers []string
	for i, r := range rows {
		if i > 0 && rows[i].Start != rows[i-1].End {
			t.Fatalf("gap between rows %d and %d", i-1, i)
		}
		ft := r.Text("ContentText")
		if ft == nil || ft.Key == "" {
			continue
		}
		keys = append(keys, ft.Key)
		sp, ok := r.Field("Speaker")
		if !ok || !sp.Present {
			t.Fatalf("row %d has a key but no speaker", i)
		}
		speakers = append(speakers, sp.Name2.String(pkg.Names))
	}
	if len(keys) != 9 {
		t.Fatalf("decoded %d keyed rows, want 9", len(keys))
	}
	for i := range keys {
		if !strings.HasSuffix(keys[i], wantKeys[i]) {
			t.Fatalf("key %d is %q, want suffix %s", i, keys[i], wantKeys[i])
		}
		if speakers[i] != wantSpeakers[i] {
			t.Fatalf("row %d speaker %q, want %q", i, speakers[i], wantSpeakers[i])
		}
	}
	t.Logf("rows=%d payload=%d first=%s last=%s", len(rows), len(payload),
		rows[0].Text("ContentText").Key, rows[len(rows)-1].Text("ContentText").Key)
}

// TestSchemaRowsGilgamesh03 checks the second independent scene the same way.
func TestSchemaRowsGilgamesh03(t *testing.T) {
	m, props := loadSchema(t, "TalkEventRowData")
	pkg, payload, pro := tablePayload(t, "DT_SubEv_C09_Gilgamesh_03")
	rows, err := zen.ParseSchemaRows(payload, pro.RowOffset, props, m, pkg.Names)
	if err != nil {
		t.Fatalf("schema rows: %v", err)
	}
	if len(rows) != 20 {
		t.Fatalf("decoded %d rows, want 20", len(rows))
	}
	if rows[len(rows)-1].End != len(payload) {
		t.Fatalf("rows end at %d, payload is %d bytes", rows[len(rows)-1].End, len(payload))
	}
	speakers := map[string]int{}
	keyed := 0
	for _, r := range rows {
		if ft := r.Text("ContentText"); ft != nil && ft.Key != "" {
			keyed++
		}
		sp, ok := r.Field("Speaker")
		if ok && sp.Present {
			speakers[sp.Name2.String(pkg.Names)]++
		}
	}
	if keyed != 10 {
		t.Fatalf("decoded %d keyed rows, want 10", keyed)
	}
	if speakers["ギルガメッシュ"] == 0 {
		t.Fatalf("no ギルガメッシュ row decoded: %v", speakers)
	}
	t.Logf("rows=%d payload=%d speakers=%v", len(rows), len(payload), speakers)
}

// TestSchemaTalkEventRowData pins the schema itself: the USMAP must describe TalkEventRowData as ten
// properties in this exact order and type, over a base struct with no fields.
func TestSchemaTalkEventRowData(t *testing.T) {
	m, props := loadSchema(t, "TalkEventRowData")
	want := []struct{ name, typ string }{
		{"TalkId", "IntProperty"},
		{"WindowType", "EnumProperty"},
		{"Speaker", "NameProperty"},
		{"Emotion", "EnumProperty"},
		{"WindowPosition", "EnumProperty"},
		{"ContentText", "TextProperty"},
		{"Voice", "StrProperty"},
		{"TextDisplayInterval", "FloatProperty"},
		{"IsLogginDisable", "BoolProperty"},
		{"IsVoiceValid", "BoolProperty"},
	}
	if len(props) != len(want) {
		t.Fatalf("schema has %d properties, want %d", len(props), len(want))
	}
	for i, w := range want {
		if props[i].Name != w.name || props[i].Type != w.typ {
			t.Fatalf("property %d is %s/%s, want %s/%s", i, props[i].Name, props[i].Type, w.name, w.typ)
		}
	}
	if props[1].Enum != "ETalkEventWindowType" || props[1].EnumInner != "ByteProperty" {
		t.Fatalf("WindowType enum is %s/%s", props[1].Enum, props[1].EnumInner)
	}
	base, ok := m.Find("MuchaTableRowBase")
	if !ok {
		t.Fatal("MuchaTableRowBase missing from the map")
	}
	if len(base.Properties) != 0 {
		t.Fatalf("MuchaTableRowBase has %d properties, want 0", len(base.Properties))
	}
	if base.Super == "" {
		t.Fatal("MuchaTableRowBase has no parent recorded")
	}
	t.Logf("base=%s -> %s, %d properties", base.Name, base.Super, len(props))
}

// TestSchemaChatRowData reads a ChatRowData table end to end: seven rows whose row names are the
// dialogue keys themselves, alternating between two speakers, tiling the payload exactly.
func TestSchemaChatRowData(t *testing.T) {
	m, props := loadSchema(t, "ChatRowData")
	if len(props) != 4 {
		t.Fatalf("ChatRowData has %d properties, want 4", len(props))
	}
	pkg, payload, pro := tablePayload(t, "DT_ATC03_TalkEvent")
	rows, err := zen.ParseSchemaRows(payload, pro.RowOffset, props, m, pkg.Names)
	if err != nil {
		t.Fatalf("schema rows: %v", err)
	}
	if len(rows) != 7 {
		t.Fatalf("decoded %d rows, want 7", len(rows))
	}
	if rows[len(rows)-1].End != len(payload) {
		t.Fatalf("rows end at %d, payload %d", rows[len(rows)-1].End, len(payload))
	}
	want := []string{"ジェイク", "ラスウェル", "ジェイク", "ラスウェル", "ジェイク", "ラスウェル", "ジェイク"}
	for i, r := range rows {
		name := r.Name.String(pkg.Names)
		if name != fmt.Sprintf("ATC03_%03d", i+1) {
			t.Fatalf("row %d name %q, want ATC03_%03d", i, name, i+1)
		}
		ft := r.Text("ContentText")
		if ft == nil || ft.Key != name {
			t.Fatalf("row %d key %v, want %q", i, ft, name)
		}
		sp, ok := r.Field("Speaker")
		if !ok || !sp.Present || sp.Name2.String(pkg.Names) != want[i] {
			t.Fatalf("row %d speaker %v, want %q", i, sp.Name2.String(pkg.Names), want[i])
		}
	}
}

// TestSchemaRowStreamMarker pins the four-byte marker that closes a dialogue row stream: a table whose
// rows stop exactly four bytes early is accepted only when those bytes are the measured marker, and a
// table ending on any other value is still refused.
func TestSchemaRowStreamMarker(t *testing.T) {
	m, props := loadSchema(t, "TalkEventRowData")
	pkg, payload, pro := tablePayload(t, "DT_INN")
	rows, err := zen.ParseSchemaRows(payload, pro.RowOffset, props, m, pkg.Names)
	if err == nil {
		t.Fatalf("DT_INN parsed without the marker rule: %d rows", len(rows))
	}
	if len(rows) == 0 {
		t.Fatal("no rows decoded before the trailer")
	}
	end := rows[len(rows)-1].End
	if end != len(payload)-4 {
		t.Fatalf("rows end at %d, payload %d (want a four-byte trailer)", end, len(payload))
	}
	if got := binary.LittleEndian.Uint32(payload[end:]); got != 0x01BF8000 {
		t.Fatalf("trailer is %#x, want 0x01BF8000", got)
	}
	// With the marker rule the same table must decode through the production entry point.
	decoded, err := zen.ParseDataTableSchemaFor(payload, pro, "TalkEventRowData", m, pkg.Names)
	if err != nil {
		t.Fatalf("marker-closed table refused: %v", err)
	}
	if len(decoded) != len(rows) {
		t.Fatalf("decoded %d rows, expected %d", len(decoded), len(rows))
	}
	// Corrupting the marker must make the table fail again rather than pass silently.
	broken := append([]byte(nil), payload...)
	binary.LittleEndian.PutUint32(broken[len(broken)-4:], 0xDEADBEEF)
	if _, err := zen.ParseDataTableSchemaFor(broken, pro, "TalkEventRowData", m, pkg.Names); err == nil {
		t.Fatal("accepted a table whose trailer is not the marker")
	}
}

// TestSchemaDialogueTableShapes pins the read on several different dialogue tables at once: the row
// count, how many rows carry a string-table key, and the speaker labels. These are real assets from
// the shipped build, so a change in framing or in the schema order breaks this test.
func TestSchemaDialogueTableShapes(t *testing.T) {
	m, tprops := loadSchema(t, "TalkEventRowData")
	cprops, err := m.Flatten("ChatRowData")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		asset    string
		chat     bool
		rows     int
		keys     int
		firstSpk string
	}{
		{"DT_SubEv_C09_Gilgamesh_01", false, 18, 9, ""},
		{"DT_SubEv_C09_Gilgamesh_03", false, 20, 10, ""},
		{"DT_C05_522_01_TalkEvent", false, 42, 21, ""},
		{"DT_INN", false, 161, 72, ""},
		{"DT_ATC03_TalkEvent", true, 7, 7, "ジェイク"},
	}
	for _, c := range cases {
		props := tprops
		if c.chat {
			props = cprops
		}
		pkg, payload, pro := tablePayload(t, c.asset)
		rows, err := zen.ParseSchemaRows(payload, pro.RowOffset, props, m, pkg.Names)
		if err != nil && len(rows) == 0 {
			t.Fatalf("%s: %v", c.asset, err)
		}
		if len(rows) != c.rows {
			t.Fatalf("%s: %d rows, want %d", c.asset, len(rows), c.rows)
		}
		end := rows[len(rows)-1].End
		if end != len(payload) && !(len(payload)-end == 4 && binary.LittleEndian.Uint32(payload[end:]) == 0x01BF8000) {
			t.Fatalf("%s: rows end at %d of %d without the marker", c.asset, end, len(payload))
		}
		keys, first := 0, ""
		for _, r := range rows {
			if ft := r.Text("ContentText"); ft != nil && ft.Key != "" {
				keys++
				if first == "" {
					if sp, ok := r.Field("Speaker"); ok && sp.Present {
						first = sp.Name2.String(pkg.Names)
					}
				}
			}
		}
		if keys != c.keys {
			t.Fatalf("%s: %d keyed rows, want %d", c.asset, keys, c.keys)
		}
		if c.firstSpk != "" && first != c.firstSpk {
			t.Fatalf("%s: first speaker %q, want %q", c.asset, first, c.firstSpk)
		}
	}
}

// TestSchemaRecordWrapper pins the wrapper that surrounds a TalkEventRowData content row: the marker
// 0x01BF8000 appears twice per content row — at its first byte and four bytes before its end — and never
// in a ChatRowData table. This is what the "twelve bytes" between two blocks actually are.
func TestSchemaRecordWrapper(t *testing.T) {
	m, tprops := loadSchema(t, "TalkEventRowData")
	pkg, payload, pro := tablePayload(t, "DT_SubEv_C09_Gilgamesh_01")
	rows, err := zen.ParseSchemaRows(payload, pro.RowOffset, tprops, m, pkg.Names)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	marker := []byte{0x00, 0x80, 0xbf, 0x01}
	count := 0
	for p := 0; p+4 <= len(payload); p++ {
		if bytes.Equal(payload[p:p+4], marker) {
			count++
		}
	}
	content := 0
	for _, r := range rows {
		if ft := r.Text("ContentText"); ft != nil && ft.Key != "" {
			content++
			if !bytes.Equal(payload[r.Start:r.Start+4], marker) {
				t.Fatalf("row at %d does not open with the wrapper", r.Start)
			}
			if !bytes.Equal(payload[r.End-4:r.End], marker) {
				t.Fatalf("row at %d does not close with the wrapper", r.Start)
			}
		}
	}
	if content != 9 || count != 18 {
		t.Fatalf("content rows %d, markers %d; want 9 and 18", content, count)
	}
	// A ChatRowData table carries no wrapper at all.
	_, chat, cpro := tablePayload(t, "DT_ATC03_TalkEvent")
	for p := 0; p+4 <= len(chat); p++ {
		if bytes.Equal(chat[p:p+4], marker) {
			t.Fatalf("ChatRowData payload carries a wrapper at %d", p)
		}
	}
	_ = cpro
}
