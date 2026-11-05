package zen_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unburger/internal/iostore"
	"unburger/internal/oodle"
	"unburger/internal/zen"
)

const assetName = "ST_SubEv_C09_Gilgamesh_01"

func fixture(t *testing.T) (string, func([]byte, int) ([]byte, error)) {
	t.Helper()
	root := os.Getenv("FFRS_ROOT")
	if root == "" {
		dir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
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
	}
	paks := filepath.Join(root, ".local/originals/switch/updates/1.0.1/romfs/FFRS/Content/Paks")
	if _, err := os.Stat(filepath.Join(paks, "FFRS-Switch.utoc")); err != nil {
		t.Skip("no RomFS container on this host")
	}
	lib := filepath.Join(root, ".local/toolchain/vendor/oodle/liboo2corelinux64.so.9")
	if err := oodle.Load(lib); err != nil {
		t.Fatalf("oodle: %v", err)
	}
	return paks, func(c []byte, n int) ([]byte, error) { return oodle.Decode(lib, c, n) }
}

func readAsset(t *testing.T, paks string, dec func([]byte, int) ([]byte, error), suffix string) ([]byte, string) {
	t.Helper()
	c, err := iostore.Open(filepath.Join(paks, "FFRS-Switch.utoc"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, f := range c.Files() {
		if strings.HasSuffix(f.Path, suffix) {
			data, err := c.ReadFile(f, dec)
			if err != nil {
				t.Fatalf("%s: %v", f.Path, err)
			}
			return data, filepath.Base(f.Path)
		}
	}
	t.Fatalf("%s not found", suffix)
	return nil, ""
}

// TestContainerHeaderVersion proves the Zen version comes from the container header chunk,
// not from the .utoc version byte.
func TestContainerHeaderVersion(t *testing.T) {
	paks, dec := fixture(t)
	c, err := iostore.Open(filepath.Join(paks, "global.utoc"))
	if err != nil {
		t.Fatalf("global: %v", err)
	}
	_ = c
	main, err := iostore.Open(filepath.Join(paks, "FFRS-Switch.utoc"))
	if err != nil {
		t.Fatalf("main: %v", err)
	}
	found := false
	for _, e := range main.Entries() {
		if e.ID.Type() != 6 {
			continue
		}
		data, err := main.Read(e.ID, dec)
		if err != nil {
			t.Fatalf("container header: %v", err)
		}
		h, err := zen.ParseContainerHeader(data)
		if err != nil {
			t.Fatalf("parse container header: %v", err)
		}
		if h.Version != zen.HeaderSoftPackageReferencesOffs {
			t.Fatalf("version %v, want SoftPackageReferencesOffset", h.Version)
		}
		if main.Version != 8 {
			t.Fatalf("toc version %d, want 8", main.Version)
		}
		found = true
		break
	}
	if !found {
		t.Fatal("no container header chunk")
	}
}

// TestZenPackage reads a real ST_ package: summary, name map, import and export tables.
func TestZenPackage(t *testing.T) {
	paks, dec := fixture(t)
	data, _ := readAsset(t, paks, dec, assetName+".uasset")
	pkg, err := zen.ParsePackage(data, zen.HeaderSoftPackageReferencesOffs, assetName)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	name := pkg.Named(pkg.Summary.Name)
	if !strings.HasSuffix(name, "/"+assetName) {
		t.Fatalf("package name %q does not end with %q", name, assetName)
	}
	if !pkg.HasCellOffsets {
		t.Fatal("expected the UE5.6 cell offsets to be present")
	}
	if len(pkg.Exports) != 1 {
		t.Fatalf("exports %d, want 1", len(pkg.Exports))
	}
	if len(pkg.Imports) != 3 {
		t.Fatalf("imports %d, want 3", len(pkg.Imports))
	}
	if len(pkg.Bundle) != 2 {
		t.Fatalf("bundle entries %d, want 2", len(pkg.Bundle))
	}
	e := pkg.Exports[0]
	if pkg.Named(e.ObjectName) != assetName {
		t.Fatalf("export name %q", pkg.Named(e.ObjectName))
	}
	if e.CookedSerialSize == 0 || e.CookedSerialSize > uint64(len(data)) {
		t.Fatalf("export size %d does not fit the %d byte package", e.CookedSerialSize, len(data))
	}
	if pkg.Bundle[0].CommandType != zen.CommandCreate || pkg.Bundle[1].CommandType != zen.CommandSerialize {
		t.Fatalf("unexpected bundle commands: %+v", pkg.Bundle)
	}
	for _, imp := range pkg.Imports {
		if imp.Kind() == zen.IndexExport || imp.IsNull() {
			t.Fatalf("import classified as %d", imp.Kind())
		}
	}
}

// TestExportBounds checks every export payload lies inside the package.
func TestExportBounds(t *testing.T) {
	paks, dec := fixture(t)
	data, _ := readAsset(t, paks, dec, "DT_Colosseum_BossBattleEvent.uasset")
	pkg, err := zen.ParsePackage(data, zen.HeaderSoftPackageReferencesOffs, "DT_Colosseum_BossBattleEvent")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(pkg.Exports) == 0 {
		t.Fatal("no exports")
	}
	for i, e := range pkg.Exports {
		if e.CookedSerialOffset+e.CookedSerialSize > uint64(len(data)) {
			t.Fatalf("export %d payload [%d,%d) outside the %d byte package",
				i, e.CookedSerialOffset, e.CookedSerialOffset+e.CookedSerialSize, len(data))
		}
	}
	t.Logf("%s: %d exports, names=%d", assetName, len(pkg.Exports), len(pkg.Names))
}

// TestScriptObjects reads the real global container and requires the table to be complete:
// every name resolves, every global index is unique, every outer resolves or is null, and the
// chains terminate.
func TestScriptObjects(t *testing.T) {
	paks, dec := fixture(t)
	g, err := iostore.Open(filepath.Join(paks, "global.utoc"))
	if err != nil {
		t.Fatalf("global: %v", err)
	}
	var data []byte
	for _, e := range g.Entries() {
		if e.ID.Type() == 5 {
			data, err = g.Read(e.ID, dec)
			if err != nil {
				t.Fatalf("script objects chunk: %v", err)
			}
			break
		}
	}
	if data == nil {
		t.Fatal("no ScriptObjects chunk in the global container")
	}
	so, err := zen.ParseScriptObjects(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(so.GlobalNames) != 48127 || len(so.Entries) != 52287 {
		t.Fatalf("names %d entries %d, want 48127/52287", len(so.GlobalNames), len(so.Entries))
	}
	// The parse must consume the chunk exactly: count + entries end at the last byte.
	if consumed := 1680490 + 4 + 52287*32; consumed != len(data) {
		t.Fatalf("table ends at %d of %d bytes", consumed, len(data))
	}
}

// TestClassesResolve proves PackageName -> ExportName -> ClassName on two independent
// packages, and rejects unresolved classes instead of guessing.
func TestClassesResolve(t *testing.T) {
	paks, dec := fixture(t)
	g, err := iostore.Open(filepath.Join(paks, "global.utoc"))
	if err != nil {
		t.Fatal(err)
	}
	var so *zen.ScriptObjects
	for _, e := range g.Entries() {
		if e.ID.Type() != 5 {
			continue
		}
		data, err := g.Read(e.ID, dec)
		if err != nil {
			t.Fatal(err)
		}
		so, err = zen.ParseScriptObjects(data)
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	if so == nil {
		t.Fatal("no script objects")
	}
	cases := []struct{ asset, class string }{
		{assetName, "StringTable"},
		{"DT_Colosseum_BossBattleEvent", "DataTable"},
	}
	for _, tc := range cases {
		data, _ := readAsset(t, paks, dec, tc.asset+".uasset")
		pkg, err := zen.ParsePackage(data, zen.HeaderSoftPackageReferencesOffs, tc.asset)
		if err != nil {
			t.Fatalf("%s: %v", tc.asset, err)
		}
		r := &zen.Resolver{Pkg: pkg, Scripts: so}
		if len(pkg.Exports) == 0 {
			t.Fatalf("%s: no exports", tc.asset)
		}
		cls, ok := r.ClassName(pkg.Exports[0])
		if !ok {
			t.Fatalf("%s: class did not resolve", tc.asset)
		}
		if cls != tc.class {
			t.Fatalf("%s: class %q, want %q", tc.asset, cls, tc.class)
		}
		for i, imp := range pkg.Imports {
			if got := r.Resolve(imp); strings.Contains(got, "unresolved") {
				t.Fatalf("%s: import %d unresolved: %s", tc.asset, i, got)
			}
		}
	}
}

// TestScriptObjectsRejectsCorruption requires hard errors for a bad count, an out-of-range
// name index, a duplicate global index and an outer cycle.
func TestScriptObjectsRejectsCorruption(t *testing.T) {
	paks, dec := fixture(t)
	g, err := iostore.Open(filepath.Join(paks, "global.utoc"))
	if err != nil {
		t.Fatal(err)
	}
	var data []byte
	for _, e := range g.Entries() {
		if e.ID.Type() == 5 {
			data, _ = g.Read(e.ID, dec)
			break
		}
	}
	if _, err := zen.ParseScriptObjects(data); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	corrupt := func(f func([]byte)) []byte {
		b := append([]byte(nil), data...)
		f(b)
		return b
	}
	le := binary.LittleEndian
	const count = 1680490
	const entries = count + 4

	if _, err := zen.ParseScriptObjects(corrupt(func(b []byte) { le.PutUint32(b[count:], 0x7fffffff) })); err == nil {
		t.Fatal("accepted an impossible entry count")
	}
	if _, err := zen.ParseScriptObjects(corrupt(func(b []byte) { le.PutUint32(b[entries:], 0x3fffffff) })); err == nil {
		t.Fatal("accepted an out-of-range name index")
	}
	if _, err := zen.ParseScriptObjects(corrupt(func(b []byte) {
		copy(b[entries+32+8:], b[entries+8:entries+16])
	})); err == nil {
		t.Fatal("accepted a duplicate global index")
	}
	if _, err := zen.ParseScriptObjects(corrupt(func(b []byte) {
		le.PutUint64(b[entries+16:], le.Uint64(b[entries+8:]))
	})); err == nil {
		t.Fatal("accepted an outer cycle")
	}
}

// TestStringTable reads real ST_ packages and requires the namespace, the keys and the
// Japanese text to survive the round trip through the Zen export payload.
func TestStringTable(t *testing.T) {
	paks, dec := fixture(t)
	cases := []struct {
		asset   string
		entries int
		first   string
		ns      string
	}{
		{assetName, 9, "SubEv_C09_Gilgamesh_01_001", "ST_SubEv_C09_Gilgamesh_01"},
		{"ST_ChrEv_2630_004_01", 31, "ChrEv_2630_004_01_001", "ST_ChrEv_2630_004_01"},
		{"ST_ATC29", 8, "ATC29_001", "ST_ATC29"},
	}
	for _, tc := range cases {
		data, _ := readAsset(t, paks, dec, tc.asset+".uasset")
		pkg, err := zen.ParsePackage(data, zen.HeaderSoftPackageReferencesOffs, tc.asset)
		if err != nil {
			t.Fatalf("%s: %v", tc.asset, err)
		}
		payload, err := pkg.ExportPayload(data, 0)
		if err != nil {
			t.Fatalf("%s: payload: %v", tc.asset, err)
		}
		st, err := zen.ParseStringTable(payload)
		if err != nil {
			t.Fatalf("%s: table: %v", tc.asset, err)
		}
		if st.Namespace != tc.ns {
			t.Fatalf("%s: namespace %q, want %q", tc.asset, st.Namespace, tc.ns)
		}
		if len(st.Keys) != tc.entries {
			t.Fatalf("%s: %d entries, want %d", tc.asset, len(st.Keys), tc.entries)
		}
		if st.Keys[0] != tc.first {
			t.Fatalf("%s: first key %q, want %q", tc.asset, st.Keys[0], tc.first)
		}
		for i := range st.Keys {
			if st.Keys[i] == "" || st.Values[i] == "" {
				t.Fatalf("%s: empty key or value at %d", tc.asset, i)
			}
			if !utf16ish(st.Values[i]) {
				t.Fatalf("%s: value %d does not look like Japanese: %q", tc.asset, i, st.Values[i])
			}
		}
	}
}

// utf16ish requires at least one non-ASCII letter, which every Japanese line has.
func utf16ish(s string) bool {
	for _, r := range s {
		if r > 0x2FFF {
			return true
		}
	}
	return false
}

// TestStringTableMatchesCorpus crosses the IoStore asset against the imported LOCRES corpus:
// the keys must be identical and the Japanese text must match byte for byte, which proves the
// asset and the corpus describe the same rows.
func TestStringTableMatchesCorpus(t *testing.T) {
	paks, dec := fixture(t)
	idx := requireCanonicalCorpus(t)
	data, _ := readAsset(t, paks, dec, assetName+".uasset")
	pkg, err := zen.ParsePackage(data, zen.HeaderSoftPackageReferencesOffs, assetName)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := pkg.ExportPayload(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	st, err := zen.ParseStringTable(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, r := range idx.ByNS[st.Namespace] {
		if r.Text.JP != nil {
			want[r.Source.Key] = *r.Text.JP
		}
	}
	if len(want) != len(st.Keys) {
		t.Fatalf("corpus has %d keys for %s, asset has %d", len(want), st.Namespace, len(st.Keys))
	}
	exact, trimmedOnly := 0, 0
	for i, k := range st.Keys {
		text, ok := want[k]
		if !ok {
			t.Fatalf("key %q missing from the corpus", k)
		}
		switch {
		case text == st.Values[i]:
			exact++
		case strings.TrimSpace(text) == strings.TrimSpace(st.Values[i]):
			// Same text but different surrounding whitespace: the asset must keep the
			// original bytes, so this is reported as its own failure, never as a match.
			trimmedOnly++
			t.Errorf("whitespace-only difference for %q: asset %q corpus %q", k, st.Values[i], text)
		default:
			t.Fatalf("text differs for %q", k)
		}
	}
	if trimmedOnly != 0 || exact != len(st.Keys) {
		t.Fatalf("exact matches %d, whitespace-only %d, keys %d", exact, trimmedOnly, len(st.Keys))
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// TestDataTablePrologue proves the four-byte FPackageIndex reading of the RowStruct reference on
// three tables with different import orders, and pins the start of the table data.
func TestDataTablePrologue(t *testing.T) {
	paks, dec := fixture(t)
	g, err := iostore.Open(filepath.Join(paks, "global.utoc"))
	if err != nil {
		t.Fatal(err)
	}
	var so *zen.ScriptObjects
	for _, e := range g.Entries() {
		if e.ID.Type() != 5 {
			continue
		}
		d, err := g.Read(e.ID, dec)
		if err != nil {
			t.Fatal(err)
		}
		so, err = zen.ParseScriptObjects(d)
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	cases := []struct {
		asset      string
		index      int32
		structName string
	}{
		{"DT_SubEv_C09_Gilgamesh_01", -6, "script:TalkEventRowData outer:script:/Script/mucha"},
		{"DT_SubEv_C09_Gilgamesh_02", -6, "script:TalkEventRowData outer:script:/Script/mucha"},
		{"DT_Colosseum_BossBattleEvent", -5, "script:Colosseum_BossEventData outer:script:/Script/mucha"},
	}
	for _, tc := range cases {
		data, _ := readAsset(t, paks, dec, tc.asset+".uasset")
		pkg, err := zen.ParsePackage(data, zen.HeaderSoftPackageReferencesOffs, tc.asset)
		if err != nil {
			t.Fatalf("%s: %v", tc.asset, err)
		}
		r := &zen.Resolver{Pkg: pkg, Scripts: so}
		payload, err := pkg.ExportPayload(data, 0)
		if err != nil {
			t.Fatalf("%s: payload: %v", tc.asset, err)
		}
		p, err := zen.ParseDataTablePrologue(payload, func(pi zen.PackageIndex) (string, bool) {
			return pkg.ResolvePackageIndex(pi, r)
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.asset, err)
		}
		if p.Header.Values != 1 {
			t.Fatalf("%s: %d properties, want 1", tc.asset, p.Header.Values)
		}
		if int32(p.RowStruct) != tc.index {
			t.Fatalf("%s: RowStruct %d, want %d", tc.asset, p.RowStruct, tc.index)
		}
		if p.RowStructName != tc.structName {
			t.Fatalf("%s: RowStruct resolves to %q, want %q", tc.asset, p.RowStructName, tc.structName)
		}
		if p.DataOffset != 6 {
			t.Fatalf("%s: data starts at %d, want 6", tc.asset, p.DataOffset)
		}
	}
}
