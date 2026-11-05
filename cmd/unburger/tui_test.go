package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"unburger/internal/build"
	"unburger/internal/corpuspack"
	"unburger/internal/variant"
)

const tuiFixture = `{
  "schema_version": 1,
  "group": {"type": "dialogue_line", "namespace": "ST_Test_01"},
  "records": [
    {
      "record_id": "rec-1",
      "source": {"set": "set-02", "namespace": "ST_Test_01", "key": "Test_001"},
      "text": {"jp": "おはよう。", "official_en": "Good morning.", "real_en": "Morning."},
      "review": {"status": "UNREVIEWED"},
      "function": "dialogue_line",
      "context": {"speaker": {"applicability": "REQUIRED", "label_jp": "レイン", "label_status": "VERIFIED", "name_entry": "CharaName0002"}}
    }
  ]
}`

func fixtureRoot(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "translations")
	p := filepath.Join(dir, "dialogue", "unassigned", "ST_Test_01.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(tuiFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := corpuspack.RefreshManifest(dir); err != nil {
		t.Fatal(err)
	}
	return root, dir
}

func fixtureModel(t *testing.T) *model {
	t.Helper()
	root, dir := fixtureRoot(t)
	idx, err := corpuspack.LoadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := &model{root: root, dir: dir, idx: idx, stats: idx.Stats, edition: "canonical", layout: "emulator", snapshot: time.Now()}
	m.files = readManifest(dir)
	m.buildTree()
	m.applyFilter()
	m.loadVariant()
	return m
}

// TestTUILoadsOnDemand: the tree comes from the manifest alone, and records are read only when a file is opened.
func TestTUILoadsOnDemand(t *testing.T) {
	m := fixtureModel(t)
	if len(m.nodes) == 0 {
		t.Fatal("tree is empty")
	}
	if len(m.records) != 0 {
		t.Fatal("records were read before a file was opened")
	}
	m.openNode(m.nodes[0])
	if len(m.records) != 1 {
		t.Fatalf("records = %d, want 1", len(m.records))
	}
	m.renderPreview()
	if m.preview.View() == "" {
		t.Fatal("preview is empty after opening a file")
	}
}

// assertLoadStatus checks the dashboard distinguishes a successful corpus load from a validation result. Loading
// parses and counts records; it is not corpus validation, so only the load state may be reported.
func assertLoadStatus(t *testing.T, view string) {
	t.Helper()
	if !strings.Contains(view, "corpus: LOADED") {
		t.Fatalf("dashboard does not report the corpus load state:\n%s", view)
	}
	if !strings.Contains(view, "validation: NOT RUN") {
		t.Fatalf("dashboard does not report that validation was not run:\n%s", view)
	}
	if strings.Contains(view, "validate PASS") || strings.Contains(view, "corpus validate") {
		t.Fatalf("dashboard claims a validation pass it did not perform:\n%s", view)
	}
}

// TestDashboardReportsLoadWithoutValidationClaim: the startup snapshot comes from LoadIndex, which parses every
// file and counts records but does not check identities, hashes or editorial status. The dashboard reports the
// load state and that validation was not run.
func TestDashboardReportsLoadWithoutValidationClaim(t *testing.T) {
	m := fixtureModel(t)
	assertLoadStatus(t, m.render())
}

// TestNewModelReportsLoadWithoutValidationClaim: the constructor follows the same reporting rule as the fixture.
func TestNewModelReportsLoadWithoutValidationClaim(t *testing.T) {
	root, _ := fixtureRoot(t)
	m, err := newModel(root)
	if err != nil {
		t.Fatal(err)
	}
	assertLoadStatus(t, m.render())
}

// TestReloadReportsLoadWithoutValidationClaim: "r" replaces the snapshot, but no validation state is stored, so
// the refreshed dashboard cannot retain or resurrect a stale pass.
func TestReloadReportsLoadWithoutValidationClaim(t *testing.T) {
	m := fixtureModel(t)
	assertLoadStatus(t, m.render())
	second := strings.NewReplacer("rec-1", "rec-2", "ST_Test_01", "ST_Test_02").Replace(tuiFixture)
	p := filepath.Join(m.dir, "dialogue", "unassigned", "ST_Test_02.json")
	if err := os.WriteFile(p, []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := corpuspack.RefreshManifest(m.dir); err != nil {
		t.Fatal(err)
	}
	before := m.snapshot
	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: 'r', Text: "r"}); cmd != nil {
		t.Fatal("reload key returned a command")
	}
	if m.stats.Records != 2 {
		t.Fatalf("after reload records = %d, want 2", m.stats.Records)
	}
	if !m.snapshot.After(before) {
		t.Fatalf("snapshot did not advance: %s -> %s", before, m.snapshot)
	}
	assertLoadStatus(t, m.render())
}

// TestTUISearchFilters: search narrows the record list over japanese, official english, id and source keys.
func TestTUISearchFilters(t *testing.T) {
	m := fixtureModel(t)
	m.openNode(m.nodes[0])
	for _, q := range []string{"おはよう", "Good morning", "rec-1", "Test_001", "ST_Test_01"} {
		m.search.SetValue(q)
		m.applyFilter()
		if len(m.filtered) != 1 {
			t.Fatalf("query %q matched %d records, want 1", q, len(m.filtered))
		}
	}
	m.search.SetValue("no such text")
	m.applyFilter()
	if len(m.filtered) != 0 {
		t.Fatalf("a non-matching query matched %d records", len(m.filtered))
	}
}

// TestTUIKeys: navigation keys move between screens and toggle the two selectors without touching the corpus.
func TestTUIKeys(t *testing.T) {
	m := fixtureModel(t)
	if m.screen != screenDashboard {
		t.Fatal("default screen is not the dashboard")
	}
	if _, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab}); m.screen != screenBrowser {
		t.Fatalf("tab did not open the browser, screen = %d", m.screen)
	}
	if _, _ = m.handleKey(tea.KeyPressMsg{Code: 'n', Text: "n"}); m.edition != "no-honorifics" {
		t.Fatalf("edition = %s", m.edition)
	}
	if _, _ = m.handleKey(tea.KeyPressMsg{Code: 'm', Text: "m"}); m.layout != "atmosphere" {
		t.Fatalf("layout = %s", m.layout)
	}
	if _, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape}); m.screen != screenDashboard {
		t.Fatal("esc did not return to the dashboard")
	}
}

func fixtureModelWithLocres(t *testing.T, present bool) *model {
	t.Helper()
	m := fixtureModel(t)
	locres := filepath.Join(m.root, ".local", "work", "locres")
	if present {
		if err := os.MkdirAll(locres, 0o755); err != nil {
			t.Fatal(err)
		}
	} else if err := os.RemoveAll(locres); err != nil {
		t.Fatal(err)
	}
	return m
}

func pcTarget(edition, state, reason string) build.Target {
	return build.Target{Platform: build.PlatformPC, Layout: build.LayoutPC, Edition: edition, State: state, Reason: reason}
}

// TestPCDashboardBeforeBuild: with no run yet the dashboard says what can be established. A present authentic
// input makes a build available; an absent one is reported as unavailable, never as an unsupported platform and
// never as the old unconditional "pc: BLOCKED".
func TestPCDashboardBeforeBuild(t *testing.T) {
	m := fixtureModelWithLocres(t, true)
	if c := m.pcCapability(); c.State != "BUILD AVAILABLE" {
		t.Fatalf("with input, capability = %+v", c)
	}
	view := m.render()
	if !strings.Contains(view, "pc build:") || !strings.Contains(view, "BUILD AVAILABLE") {
		t.Fatalf("dashboard does not report build availability:\n%s", view)
	}
	if strings.Contains(view, "pc: BLOCKED") || strings.Contains(view, "unsupported") {
		t.Fatalf("dashboard still calls the PC platform blocked:\n%s", view)
	}

	m = fixtureModelWithLocres(t, false)
	c := m.pcCapability()
	if c.State != "UNAVAILABLE" || c.Reason == "" {
		t.Fatalf("without input, capability = %+v", c)
	}
	if view := m.render(); strings.Contains(view, "pc: BLOCKED") || !strings.Contains(view, "UNAVAILABLE") {
		t.Fatalf("missing input is not reported honestly:\n%s", view)
	}
}

// TestPCCanonicalSuccessReporting: a successful PC canonical target is shown as a real build pass.
func TestPCCanonicalSuccessReporting(t *testing.T) {
	m := fixtureModelWithLocres(t, true)
	m.targets = []build.Target{pcTarget(build.EditionCanonical, build.StatePass, "")}
	if c := m.pcCapability(); c.State != "BUILD PASS" || c.Reason != "" {
		t.Fatalf("capability = %+v", c)
	}
	view := m.render()
	if !strings.Contains(view, "BUILD PASS") {
		t.Fatalf("dashboard does not report the PC pass:\n%s", view)
	}
}

// TestPCNoHonorificsSuccessReporting: the variant edition reports the same way when its overlay was approved.
func TestPCNoHonorificsSuccessReporting(t *testing.T) {
	m := fixtureModelWithLocres(t, true)
	m.targets = []build.Target{pcTarget(build.EditionNoHonorifics, build.StatePass, "")}
	if c := m.pcCapability(); c.State != "BUILD PASS" {
		t.Fatalf("capability = %+v", c)
	}
	if view := m.render(); !strings.Contains(view, "BUILD PASS") {
		t.Fatalf("dashboard does not report the variant pass:\n%s", view)
	}
}

// TestPCAggregateSuccessReporting: a full run with both PC editions passing stays one BUILD PASS, not a
// per-edition split that could make a canonical success look like a platform failure.
func TestPCAggregateSuccessReporting(t *testing.T) {
	m := fixtureModelWithLocres(t, true)
	m.targets = []build.Target{
		pcTarget(build.EditionCanonical, build.StatePass, ""),
		pcTarget(build.EditionNoHonorifics, build.StatePass, ""),
	}
	if c := m.pcCapability(); c.State != "BUILD PASS" {
		t.Fatalf("capability = %+v", c)
	}
}

// TestPCFailureReporting: a genuine build failure keeps its reason and is not relabelled as a blocked platform.
func TestPCFailureReporting(t *testing.T) {
	m := fixtureModelWithLocres(t, true)
	m.targets = []build.Target{pcTarget(build.EditionCanonical, build.StateFailed, "write failed: disk full")}
	c := m.pcCapability()
	if c.State != "BUILD FAILED" || !strings.Contains(c.Reason, "disk full") {
		t.Fatalf("capability = %+v", c)
	}
	view := m.render()
	if !strings.Contains(view, "BUILD FAILED") || !strings.Contains(view, "disk full") {
		t.Fatalf("dashboard hides the failure:\n%s", view)
	}
	// A failure before per-target results exists is also reported, from the build error itself.
	m.targets = nil
	m.buildErr = "corpus: manifest is missing"
	if c := m.pcCapability(); c.State != "BUILD FAILED" || !strings.Contains(c.Reason, "manifest is missing") {
		t.Fatalf("build-level failure capability = %+v", c)
	}
}

// TestPCMissingInputReporting: a matrix BLOCKED entry caused by a missing authentic resource is shown as
// unavailable with the real reason, and the platform itself is not called unsupported.
func TestPCMissingInputReporting(t *testing.T) {
	m := fixtureModelWithLocres(t, false)
	m.targets = []build.Target{pcTarget(build.EditionCanonical, build.StateBlocked, "no authentic localisation resource")}
	c := m.pcCapability()
	if c.State != "UNAVAILABLE" || !strings.Contains(c.Reason, "authentic localisation resource") {
		t.Fatalf("capability = %+v", c)
	}
	view := m.render()
	if strings.Contains(view, "pc: BLOCKED") || strings.Contains(view, "unsupported") {
		t.Fatalf("missing input was presented as a platform restriction:\n%s", view)
	}
}

// TestPCStateIsNotStaleAndRuntimeIsSeparate: a new run replaces the old PC result instead of leaving BUILD PASS
// behind, the spinner state wins while a build runs, and a structural pass never becomes a runtime pass.
func TestPCStateIsNotStaleAndRuntimeIsSeparate(t *testing.T) {
	m := fixtureModelWithLocres(t, true)
	m.targets = []build.Target{pcTarget(build.EditionCanonical, build.StatePass, "")}
	if c := m.pcCapability(); c.State != "BUILD PASS" {
		t.Fatalf("capability = %+v", c)
	}
	m.targets = []build.Target{{Platform: build.PlatformSwitch, Layout: build.LayoutEmulator, Edition: build.EditionCanonical, State: build.StatePass}}
	if c := m.pcCapability(); c.State == "BUILD PASS" {
		t.Fatal("a later Switch-only run left the PC pass in place")
	}
	if c := m.pcCapability(); c.State != "BUILD AVAILABLE" {
		t.Fatalf("capability after a Switch-only run = %+v", c)
	}
	m.building = true
	if c := m.pcCapability(); c.State != "BUILDING" {
		t.Fatalf("capability while building = %+v", c)
	}
	m.building = false
	m.targets = []build.Target{pcTarget(build.EditionCanonical, build.StatePass, "")}
	view := m.render()
	if !strings.Contains(view, "BUILD PASS") {
		t.Fatalf("no build pass in view:\n%s", view)
	}
	if !strings.Contains(view, "runtime:") || !strings.Contains(view, build.RuntimeUnknown) {
		t.Fatalf("runtime state is not separated from the build result:\n%s", view)
	}
	if strings.Contains(view, "RUNTIME PASS") {
		t.Fatalf("a structural build claimed a runtime pass:\n%s", view)
	}
}

func variantFileWith(overrides ...variant.Override) variant.File {
	return variant.File{
		SchemaVersion: variant.SchemaVersion,
		Variant:       variant.NoHonorifics,
		Base:          "canonical",
		Overrides:     overrides,
	}
}

func writeVariant(t *testing.T, m *model, f variant.File) {
	t.Helper()
	if err := variant.Write(variant.Path(m.dir, variant.NoHonorifics), f); err != nil {
		t.Fatal(err)
	}
	m.loadVariant()
}

// TestVariantCounterUsesValidatedOverrides: the card counts the read-and-validated overrides from the same file
// and reader the build uses. The number is available overrides, not applied ones, so both editions show it even
// though canonical applies none.
func TestVariantCounterUsesValidatedOverrides(t *testing.T) {
	m := fixtureModel(t)
	writeVariant(t, m, variantFileWith(variant.Override{
		RecordID:         "rec-1",
		BaseRealENSHA256: corpuspack.HashText("Morning."),
		RealEN:           "Morning!",
	}))
	if m.overrides != 1 || m.overridesErr != "" {
		t.Fatalf("variant count = %d, err = %q, want 1 and no error", m.overrides, m.overridesErr)
	}
	for _, edition := range []string{"canonical", "no-honorifics"} {
		m.edition = edition
		view := m.render()
		if !strings.Contains(view, "validated available") || !strings.Contains(view, "no-honorifics only") {
			t.Fatalf("edition %s does not label the counter as available overrides:\n%s", edition, view)
		}
	}
}

// TestVariantCounterMissingAndInvalid: a missing file and a malformed or stale file both show zero with a reason;
// only a valid file may use the "available" wording.
func TestVariantCounterMissingAndInvalid(t *testing.T) {
	m := fixtureModel(t)
	if m.overrides != 0 || m.overridesErr == "" {
		t.Fatalf("missing variant: count = %d, err = %q", m.overrides, m.overridesErr)
	}
	view := m.render()
	if !strings.Contains(view, "validated unavailable") || !strings.Contains(view, "variant:") {
		t.Fatalf("missing variant is not reported with a reason:\n%s", view)
	}
	if strings.Contains(view, "validated available") {
		t.Fatalf("missing variant still claims available overrides:\n%s", view)
	}

	m = fixtureModel(t)
	badSchema := variantFileWith()
	badSchema.SchemaVersion = variant.SchemaVersion + 1
	writeVariant(t, m, badSchema)
	if m.overrides != 0 || !strings.Contains(m.overridesErr, "schema version") {
		t.Fatalf("bad schema: count = %d, err = %q", m.overrides, m.overridesErr)
	}

	m = fixtureModel(t)
	writeVariant(t, m, variantFileWith(variant.Override{
		RecordID:         "rec-1",
		BaseRealENSHA256: strings.Repeat("0", 64),
		RealEN:           "Morning!",
	}))
	if m.overrides != 0 || !strings.Contains(m.overridesErr, "canonical text changed") {
		t.Fatalf("stale hash: count = %d, err = %q", m.overrides, m.overridesErr)
	}
}

// TestVariantCounterValidEmpty: a valid file with no overrides is not an error and must not be reported as
// unavailable.
func TestVariantCounterValidEmpty(t *testing.T) {
	m := fixtureModel(t)
	writeVariant(t, m, variantFileWith())
	if m.overrides != 0 || m.overridesErr != "" {
		t.Fatalf("empty variant: count = %d, err = %q", m.overrides, m.overridesErr)
	}
	view := m.render()
	if !strings.Contains(view, "validated available") || strings.Contains(view, "validated unavailable") {
		t.Fatalf("empty variant is not distinguished from an unavailable one:\n%s", view)
	}
}

// TestNewModelLoadsStartupSnapshot: runTUI's constructor reads the installed corpus and variant at startup, so the
// dashboard never starts from a different source than the build.
func TestNewModelLoadsStartupSnapshot(t *testing.T) {
	root, dir := fixtureRoot(t)
	// Write the variant before construction: startup must pick it up without a reload.
	if err := variant.Write(variant.Path(dir, variant.NoHonorifics), variantFileWith(variant.Override{
		RecordID:         "rec-1",
		BaseRealENSHA256: corpuspack.HashText("Morning."),
		RealEN:           "Morning!",
	})); err != nil {
		t.Fatal(err)
	}
	got, err := newModel(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.stats.Records != 1 || got.stats.RealEN != 1 {
		t.Fatalf("startup stats = %+v, want 1 record and 1 real translation", got.stats)
	}
	if got.overrides != 1 || got.overridesErr != "" {
		t.Fatalf("startup variant = %d, err = %q", got.overrides, got.overridesErr)
	}
	if got.snapshot.IsZero() {
		t.Fatal("startup snapshot time is zero")
	}
}

// TestReloadRefreshesCorpusAndVariant: "r" re-reads the installed corpus and variant and moves the snapshot
// timestamp, so a long-running dashboard does not keep showing the corpus it started with.
func TestReloadRefreshesCorpusAndVariant(t *testing.T) {
	m := fixtureModel(t)
	second := strings.NewReplacer("rec-1", "rec-2", "ST_Test_01", "ST_Test_02").Replace(tuiFixture)
	p := filepath.Join(m.dir, "dialogue", "unassigned", "ST_Test_02.json")
	if err := os.WriteFile(p, []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := corpuspack.RefreshManifest(m.dir); err != nil {
		t.Fatal(err)
	}
	writeVariant(t, m, variantFileWith(variant.Override{
		RecordID:         "rec-1",
		BaseRealENSHA256: corpuspack.HashText("Morning."),
		RealEN:           "Morning!",
	}))
	before := m.snapshot
	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: 'r', Text: "r"}); cmd != nil {
		t.Fatal("reload key returned a command")
	}
	if m.stats.Records != 2 {
		t.Fatalf("after reload records = %d, want 2", m.stats.Records)
	}
	if m.overrides != 1 {
		t.Fatalf("after reload overrides = %d, want 1", m.overrides)
	}
	if !m.snapshot.After(before) {
		t.Fatalf("snapshot did not advance: %s -> %s", before, m.snapshot)
	}
	if view := m.render(); !strings.Contains(view, "corpus snapshot") {
		t.Fatalf("dashboard does not state its snapshot time:\n%s", view)
	}
}

func switchTarget(layout, edition, state, reason string) build.Target {
	return build.Target{Platform: build.PlatformSwitch, Layout: layout, Edition: edition, State: state, Reason: reason}
}

// TestSwitchDashboardBeforeBuild: before any run the Switch row reports whether a build can start, and its
// structural build state is never presented as a runtime result.
func TestSwitchDashboardBeforeBuild(t *testing.T) {
	m := fixtureModelWithLocres(t, true)
	if c := m.switchCapability(); c.State != "BUILD AVAILABLE" {
		t.Fatalf("with input, switch capability = %+v", c)
	}
	view := m.render()
	if !strings.Contains(view, "switch build:") || !strings.Contains(view, "BUILD AVAILABLE") {
		t.Fatalf("dashboard does not report Switch build availability:\n%s", view)
	}
	if !strings.Contains(view, "switch runtime:") || !strings.Contains(view, build.RuntimeUnknown) {
		t.Fatalf("dashboard does not separate Switch runtime state:\n%s", view)
	}
	if strings.Contains(view, "structural only") {
		t.Fatalf("dashboard still shows the hardcoded Switch writer label:\n%s", view)
	}

	m = fixtureModelWithLocres(t, false)
	if c := m.switchCapability(); c.State != "UNAVAILABLE" || c.Reason == "" {
		t.Fatalf("without input, switch capability = %+v", c)
	}
}

// TestSwitchUsesLastRunWithRuntimeStillUnknown: the Switch row follows the last run that contained a Switch
// target, distinguishes pass/not-ready, never becomes a runtime pass, and is replaced by a later PC-only run.
func TestSwitchUsesLastRunWithRuntimeStillUnknown(t *testing.T) {
	m := fixtureModelWithLocres(t, true)
	m.targets = []build.Target{
		switchTarget(build.LayoutEmulator, build.EditionCanonical, build.StatePass, ""),
		switchTarget(build.LayoutEmulator, build.EditionNoHonorifics, build.StateNotReady, "no approved no-honorifics overrides"),
	}
	c := m.switchCapability()
	if c.State != "BUILD PASS · NOT READY" || !strings.Contains(c.Reason, "no approved") {
		t.Fatalf("mixed switch capability = %+v", c)
	}
	view := m.render()
	if !strings.Contains(view, "BUILD PASS · NOT READY") {
		t.Fatalf("dashboard hides the mixed Switch result:\n%s", view)
	}
	if strings.Contains(view, "RUNTIME PASS") || !strings.Contains(view, build.RuntimeUnknown) {
		t.Fatalf("structural Switch result leaked into runtime state:\n%s", view)
	}

	m.targets = []build.Target{pcTarget(build.EditionCanonical, build.StatePass, "")}
	if c := m.switchCapability(); c.State != "BUILD AVAILABLE" {
		t.Fatalf("a PC-only run left a stale Switch result: %+v", c)
	}

	m.building = true
	if c := m.switchCapability(); c.State != "BUILDING" {
		t.Fatalf("switch capability while building = %+v", c)
	}
}

// TestSwitchFailureAndBlockedReporting: genuine failures keep their reason; a blocked authentic input is
// unavailable, not an unsupported platform.
func TestSwitchFailureAndBlockedReporting(t *testing.T) {
	m := fixtureModelWithLocres(t, true)
	m.targets = []build.Target{switchTarget(build.LayoutAtmosphere, build.EditionCanonical, build.StateFailed, "container header does not parse back")}
	c := m.switchCapability()
	if c.State != "BUILD FAILED" || !strings.Contains(c.Reason, "container header") {
		t.Fatalf("switch failure capability = %+v", c)
	}
	view := m.render()
	if !strings.Contains(view, "switch build:") || !strings.Contains(view, "BUILD FAILED") || !strings.Contains(view, "container header") {
		t.Fatalf("dashboard hides the Switch failure:\n%s", view)
	}

	m = fixtureModelWithLocres(t, false)
	m.targets = []build.Target{switchTarget(build.LayoutEmulator, build.EditionCanonical, build.StateBlocked, "no authentic localisation resource")}
	c = m.switchCapability()
	if c.State != "UNAVAILABLE" || !strings.Contains(c.Reason, "authentic localisation resource") {
		t.Fatalf("switch blocked capability = %+v", c)
	}
}
