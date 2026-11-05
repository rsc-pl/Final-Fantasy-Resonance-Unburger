package main

// The terminal interface. It is a Bubble Tea v2 application: full screen, keyboard and mouse driven, with the
// dashboard, the translation browser and the build manager as separate screens. It reads the canonical corpus
// through internal/corpuspack and builds through internal/build — there is no second store behind it.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"unburger/internal/build"
	"unburger/internal/corpuspack"
	"unburger/internal/variant"
	"unburger/internal/version"
)

const (
	screenDashboard = iota
	screenBrowser
	screenBuild
)

// Styles are declared once so every screen shares one palette and one idea of spacing.
var (
	colAccent = lipgloss.Color("212")
	colMuted  = lipgloss.Color("244")
	colOK     = lipgloss.Color("42")
	colWarn   = lipgloss.Color("214")
	colErr    = lipgloss.Color("203")

	styleTitle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(colAccent).Padding(0, 1)
	stylePanel      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colMuted).Padding(0, 1)
	stylePanelFocus = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colAccent).Padding(0, 1)
	styleMuted      = lipgloss.NewStyle().Foreground(colMuted)
	styleAccent     = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleOK         = lipgloss.NewStyle().Foreground(colOK)
	styleWarn       = lipgloss.NewStyle().Foreground(colWarn)
	styleErr        = lipgloss.NewStyle().Foreground(colErr)
	styleSelected   = lipgloss.NewStyle().Background(lipgloss.Color("237")).Bold(true)
)

// node is one line of the corpus tree: a directory or a file.
type node struct {
	label   string
	path    string
	files   int
	records int
	isFile  bool
	depth   int
}

type keyMap struct{}

func (keyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "section")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	}
}

func (keyMap) FullHelp() [][]key.Binding { return [][]key.Binding{keyMap{}.ShortHelp()} }

type model struct {
	root string
	dir  string
	idx  *corpuspack.Index

	files []corpuspack.ManifestEntry
	stats corpuspack.Stats

	width, height int
	screen        int
	menuCursor    int
	showHelp      bool

	nodes      []node
	nodeCursor int
	records    []corpuspack.Record
	recCursor  int
	filtered   []int
	preview    viewport.Model
	search     textinput.Model
	searching  bool

	targets   []build.Target
	tbl       table.Model
	building  bool
	stage     string
	spin      spinner.Model
	edition   string
	layout    string
	ver       string
	verErr    string
	overrides int
	// overridesErr is set when the variant file is missing, malformed or fails validation. It is empty for a
	// valid file with zero overrides, so "0 available" and "unavailable" cannot be confused.
	overridesErr string
	snapshot     time.Time
	lastNote     string
	lastErr      string
	buildErr     string
	helpModel    help.Model
	buildLog     []string
	outLog       []string
}

// newModel reads the installed corpus once, then derives every dashboard value from that same snapshot. The TUI
// does not poll: "r" re-reads the corpus, the variant overlay and the version without restarting the program.
func newModel(root string) (*model, error) {
	dir := filepath.Join(root, "translations")
	idx, err := corpuspack.LoadIndex(dir)
	if err != nil {
		return nil, err
	}
	m := &model{
		root: root, dir: dir, idx: idx, stats: idx.Stats,
		screen: screenDashboard, edition: "canonical", layout: "emulator",
		preview: viewport.New(), search: textinput.New(),
		spin: spinner.New(), helpModel: help.New(), snapshot: time.Now(),
	}
	m.spin.Spinner = spinner.Dot
	m.search.Placeholder = "search jp / en / real / id / namespace / key"
	m.files = readManifest(dir)
	m.buildTree()
	m.applyFilter()
	m.loadVariant()
	m.loadVersion()
	return m, nil
}

func runTUI(root string) int {
	m, err := newModel(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "corpus:", err)
		return 1
	}
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func (m *model) loadVersion() {
	m.ver, m.verErr = "", ""
	if v, err := version.Load(filepath.Join(m.root, version.FileName)); err != nil {
		m.verErr = err.Error()
	} else {
		m.ver = v
	}
}

// variantFile is the single read/validate path shared by the dashboard counter and the build worker, so the
// number shown is exactly the set a build would apply. Validation checks every override against the canonical
// index; the dashboard counts only a file that passed it.
func (m *model) variantFile() (*variant.File, error) {
	f, err := variant.Load(variant.Path(m.dir, variant.NoHonorifics))
	if err != nil {
		return nil, err
	}
	problems, err := variant.Validate(f, m.idx)
	if err != nil {
		if len(problems) == 0 {
			return nil, err
		}
		// Keep the dashboard reason readable: name the first few defects rather than turning the status panel
		// into a dump of every stale override.
		shown := problems
		suffix := ""
		if len(shown) > 3 {
			suffix = fmt.Sprintf(" (+%d more)", len(shown)-3)
			shown = shown[:3]
		}
		return nil, fmt.Errorf("%w: %s%s", err, strings.Join(shown, "; "), suffix)
	}
	return &f, nil
}

// loadVariant exposes the validated override count. The count is the number of available overrides, not the
// number applied: Canonical applies none and No-Honorifics applies all of them, so the card is labelled
// "available" rather than "applied". A missing or invalid variant keeps the count at zero and records why.
func (m *model) loadVariant() {
	m.overrides, m.overridesErr = 0, ""
	f, err := m.variantFile()
	if err != nil {
		m.overridesErr = err.Error()
		return
	}
	m.overrides = len(f.Overrides)
}

// reload replaces the startup snapshot with the corpus and variant currently installed. It is deliberately
// all-or-nothing for the corpus: a failed reload keeps the previous snapshot and reports the error instead of
// leaving the dashboard half-refreshed.
func (m *model) reload() {
	idx, err := corpuspack.LoadIndex(m.dir)
	if err != nil {
		m.lastErr = "reload: " + err.Error()
		return
	}
	m.idx = idx
	m.stats = idx.Stats
	m.files = readManifest(m.dir)
	m.buildTree()
	m.nodeCursor = 0
	m.records = nil
	m.recCursor = 0
	m.filtered = nil
	m.preview.SetContent(styleMuted.Render("no record selected"))
	m.snapshot = time.Now()
	m.lastErr = ""
	m.loadVariant()
	m.loadVersion()
}

func readManifest(dir string) []corpuspack.ManifestEntry {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil
	}
	var m corpuspack.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m.Files
}

// buildTree turns the manifest into the visible categories and files. Nothing is loaded here beyond the manifest
// itself; the records of a file are read when that file is opened.
func (m *model) buildTree() {
	type agg struct{ files, records int }
	byDir := map[string]*agg{}
	order := []string{}
	for _, f := range m.files {
		d := filepath.Dir(f.Path)
		if byDir[d] == nil {
			byDir[d] = &agg{}
			order = append(order, d)
		}
		byDir[d].files++
		byDir[d].records += f.Records
	}
	sort.Strings(order)
	m.nodes = m.nodes[:0]
	for _, d := range order {
		m.nodes = append(m.nodes, node{label: d, files: byDir[d].files, records: byDir[d].records, path: d, depth: 0})
	}
}

// openNode loads the records of one corpus file on demand.
func (m *model) openNode(n node) {
	for _, f := range m.files {
		if f.Path == n.path {
			raw, err := os.ReadFile(filepath.Join(m.dir, filepath.FromSlash(f.Path)))
			if err != nil {
				m.lastErr = err.Error()
				return
			}
			var file corpuspack.File
			if err := json.Unmarshal(raw, &file); err != nil {
				m.lastErr = err.Error()
				return
			}
			m.records = file.Records
			m.recCursor = 0
			m.applyFilter()
			m.renderPreview()
			return
		}
	}
	// A directory opens its first file so the right pane is never empty.
	for _, f := range m.files {
		if strings.HasPrefix(f.Path, n.path+"/") {
			m.openNode(node{path: f.Path, isFile: true})
			return
		}
	}
}

func (m *model) applyFilter() {
	m.filtered = m.filtered[:0]
	q := strings.ToLower(m.search.Value())
	for i, r := range m.records {
		if q == "" {
			m.filtered = append(m.filtered, i)
			continue
		}
		hay := strings.ToLower(strings.Join([]string{
			derefStr(r.Text.JP), derefStr(r.Text.OfficialEN), derefStr(r.Text.RealEN),
			r.RecordID, r.Source.Namespace, r.Source.Key,
		}, "\n"))
		if strings.Contains(hay, q) {
			m.filtered = append(m.filtered, i)
		}
	}
	if m.recCursor >= len(m.filtered) {
		m.recCursor = max(0, len(m.filtered)-1)
	}
}

// renderPreview lays the selected record out for the right pane: three texts side by side, then the context.
func (m *model) renderPreview() {
	if m.preview.Width() < 20 {
		m.preview.SetWidth(60)
	}
	if m.preview.Height() < 5 {
		m.preview.SetHeight(20)
	}
	if len(m.filtered) == 0 {
		m.preview.SetContent(styleMuted.Render("no record selected"))
		return
	}
	r := m.records[m.filtered[m.recCursor]]
	sp := r.Context.Speaker
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", styleAccent.Render(r.RecordID))
	fmt.Fprintf(&b, "%s  %s / %s / %s\n\n", styleMuted.Render(r.Function), r.Source.Set, r.Source.Namespace, r.Source.Key)
	b.WriteString(styleMuted.Render("JP") + "\n" + wrap(derefStr(r.Text.JP), m.preview.Width()-2) + "\n\n")
	b.WriteString(styleMuted.Render("Official EN") + "\n" + wrap(derefStr(r.Text.OfficialEN), m.preview.Width()-2) + "\n\n")
	real := derefStr(r.Text.RealEN)
	if real == "" {
		real = styleMuted.Render("— no approved translation —")
	}
	b.WriteString(styleMuted.Render("RealTL EN") + "\n" + wrap(real, m.preview.Width()-2) + "\n\n")
	label := derefStr(sp.LabelJP)
	if label == "" {
		label = "—"
	}
	fmt.Fprintf(&b, "speaker  %s  status=%s  entry=%s\n", label, sp.LabelStatus, derefStr(sp.NameEntry))
	fmt.Fprintf(&b, "review   %s", r.Review.Status)
	if r.Review.Reason != nil && *r.Review.Reason != "" {
		fmt.Fprintf(&b, "  reason=%s", *r.Review.Reason)
	}
	b.WriteString("\n")
	for _, c := range sp.Candidates {
		fmt.Fprintf(&b, "conflict %s  table=%s\n", c.Label, c.Table)
	}
	m.preview.SetContent(b.String())
}

func wrap(s string, width int) string {
	if width < 10 {
		width = 10
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			out = append(out, "")
			continue
		}
		runes := []rune(line)
		for len(runes) > width {
			out = append(out, string(runes[:width]))
			runes = runes[width:]
		}
		out = append(out, string(runes))
	}
	return strings.Join(out, "\n")
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m *model) Init() tea.Cmd { return m.spin.Tick }

// Messages the build worker sends back: one per target, then one on completion.
type buildProgressMsg struct {
	stage   string
	done    int
	total   int
	targets []build.Target
}

type buildDoneMsg struct {
	targets []build.Target
	err     error
}

func (m *model) startBuild(all bool) tea.Cmd {
	if m.building {
		return nil
	}
	m.building = true
	m.stage = "validation"
	m.buildLog = m.buildLog[:0]
	m.lastErr = ""
	m.buildErr = ""
	opt := build.AllOptions{
		Version:   m.ver,
		CorpusDir: m.dir,
		LocresIn:  filepath.Join(m.root, ".local", "work", "locres"),
		DistDir:   filepath.Join(m.root, "dist"),
		WorkDir:   filepath.Join(m.root, ".local", "out"),
	}
	if !all {
		opt.Filter = build.Filter{Layout: m.layout, Edition: m.edition}
	}
	var vf *variant.File
	if v, err := m.variantFile(); err == nil {
		vf = v
	}
	return func() tea.Msg {
		targets, err := build.RunAll(context.Background(), opt, vf)
		return buildDoneMsg{targets: targets, err: err}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ensureTable()
		m.tbl.SetWidth(msg.Width - 4)
		m.tbl.SetHeight(max(4, msg.Height-14))
		m.preview.SetWidth(max(20, msg.Width-m.width/3-6))
		m.preview.SetHeight(max(5, msg.Height-8))
		m.tbl.SetWidth(msg.Width - 4)
		m.tbl.SetHeight(max(4, msg.Height-14))
		m.renderPreview()
		return m, nil
	case buildDoneMsg:
		m.building = false
		m.stage = ""
		m.buildErr = ""
		if msg.err != nil {
			m.lastErr = msg.err.Error()
			m.buildErr = msg.err.Error()
		}
		m.targets = msg.targets
		rows := make([]table.Row, 0, len(msg.targets))
		for _, t := range msg.targets {
			art := "-"
			if t.Artifact != "" {
				art = filepath.Base(t.Artifact)
			}
			rows = append(rows, table.Row{t.Platform, t.Layout, t.Edition, t.State, art})
		}
		m.tbl.SetRows(rows)
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	var cmd tea.Cmd
	if m.screen == screenBrowser {
		m.preview, cmd = m.preview.Update(msg)
	}
	return m, cmd
}

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	switch k {
	case "ctrl+c":
		return m, tea.Quit
	}
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}
	if m.searching {
		switch k {
		case "esc":
			m.searching = false
			return m, nil
		case "enter":
			m.searching = false
			m.applyFilter()
			m.renderPreview()
			return m, nil
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		m.applyFilter()
		m.renderPreview()
		return m, cmd
	}
	switch k {
	case "q":
		if m.screen == screenDashboard {
			return m, tea.Quit
		}
		m.screen = screenDashboard
		return m, nil
	case "?":
		m.showHelp = true
		return m, nil
	case "tab":
		m.screen = (m.screen + 1) % 3
		return m, nil
	case "esc":
		m.screen = screenDashboard
		return m, nil
	case "r":
		m.reload()
		return m, nil
	case "/":
		if m.screen == screenBrowser {
			m.searching = true
			m.search.Focus()
			return m, textinput.Blink
		}
	}
	switch m.screen {
	case screenDashboard:
		switch k {
		case "up", "k":
			m.menuCursor = max(0, m.menuCursor-1)
		case "down", "j":
			m.menuCursor = min(2, m.menuCursor+1)
		case "enter":
			m.screen = []int{screenBrowser, screenBrowser, screenBuild}[m.menuCursor]
		}
	case screenBrowser:
		switch k {
		case "up", "k":
			if m.nodeCursor > 0 {
				m.nodeCursor--
			}
		case "down", "j":
			if m.nodeCursor < len(m.nodes)-1 {
				m.nodeCursor++
			}
		case "enter":
			if len(m.nodes) > 0 {
				m.openNode(m.nodes[m.nodeCursor])
			}
		case "right", "l":
			m.openNode(m.nodes[m.nodeCursor])
		case "]", "pgdown":
			if m.recCursor < len(m.filtered)-1 {
				m.recCursor++
				m.renderPreview()
			}
		case "[", "pgup":
			if m.recCursor > 0 {
				m.recCursor--
				m.renderPreview()
			}
		case "n":
			m.edition = toggleEdition(m.edition)
		case "m":
			m.layout = toggleLayout(m.layout)
		}
	case screenBuild:
		switch k {
		case "b":
			return m, tea.Batch(m.startBuild(true), m.spin.Tick)
		case "enter":
			return m, tea.Batch(m.startBuild(false), m.spin.Tick)
		case "n":
			m.edition = toggleEdition(m.edition)
		case "m":
			m.layout = toggleLayout(m.layout)
		case "up", "k", "down", "j":
			var cmd tea.Cmd
			m.tbl, cmd = m.tbl.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func toggleEdition(e string) string {
	if e == "canonical" {
		return "no-honorifics"
	}
	return "canonical"
}

func toggleLayout(l string) string {
	if l == "emulator" {
		return "atmosphere"
	}
	return "emulator"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m *model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *model) render() string {
	if m.showHelp {
		return m.renderHelp()
	}
	header := styleTitle.Render("FFRS UNBURGER") + "  " + styleMuted.Render("v"+m.ver) + "  " + styleMuted.Render("canonical-split-v1") +
		"   edition: " + styleAccent.Render(m.edition) + "   layout: " + styleAccent.Render(m.layout)
	body := ""
	switch m.screen {
	case screenDashboard:
		body = m.viewDashboard()
	case screenBrowser:
		body = m.viewBrowser()
	case screenBuild:
		body = m.viewBuild()
	}
	footer := m.helpModel.View(keyMap{})
	if m.building {
		footer = m.spin.View() + " " + m.stage + "   " + footer
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// capability is what the dashboard can establish about a platform from the current inputs and the last run.
// A build result is not a runtime result, and an absent input is not evidence that a platform is unsupported.
type capability struct {
	State  string
	Reason string
}

// pcCapability derives the PC row from the last run that actually included a PC target. PC is part of the build
// matrix, so it is never reported as an unsupported platform. Before any run the row says whether a build can
// start; after a run it says what that run did, with a genuine failure or an absent input reported separately.
func (m *model) pcCapability() capability { return m.platformCapability(build.PlatformPC) }

// switchCapability mirrors the PC row from the last run that included a Switch target.
//
// What "BUILD PASS" covers for Switch: the composed locres is packed into a version-11 patch pak, read back
// through the independent pak reader, checked for mount point, virtual path, entry count and payload SHA-1,
// decoded back to the exact composed bytes, and shipped with its .utoc/.ucas triad and metadata. That is
// structural evidence only. It says nothing about whether an emulator or the console loads the package; that
// runtime state stays UNVERIFIED until it is tested on the target.
func (m *model) switchCapability() capability { return m.platformCapability(build.PlatformSwitch) }

func (m *model) platformCapability(platform string) capability {
	if m.building {
		return capability{State: "BUILDING"}
	}
	var selected []build.Target
	for _, t := range m.targets {
		if t.Platform == platform {
			selected = append(selected, t)
		}
	}
	if len(selected) > 0 {
		return summariseTargets(selected)
	}
	if m.buildErr != "" {
		return capability{State: "BUILD FAILED", Reason: m.buildErr}
	}
	if _, err := os.Stat(filepath.Join(m.root, ".local", "work", "locres")); err != nil {
		return capability{State: "UNAVAILABLE", Reason: "no authentic localisation resource"}
	}
	return capability{State: "BUILD AVAILABLE"}
}

// summariseTargets folds the targets of one platform within one run into a single honest row. Failure wins over
// an unavailable input, which wins over a not-ready edition; a mixed pass/not-ready run names both so a canonical
// success is not hidden by a variant that has no approved overrides.
func summariseTargets(targets []build.Target) capability {
	pass, failed, blocked, notReady := 0, 0, 0, 0
	var failReason, blockedReason, notReadyReason string
	for _, t := range targets {
		switch t.State {
		case build.StatePass:
			pass++
		case build.StateFailed:
			failed++
			if failReason == "" {
				failReason = t.Reason
			}
		case build.StateBlocked:
			blocked++
			if blockedReason == "" {
				blockedReason = t.Reason
			}
		case build.StateNotReady:
			notReady++
			if notReadyReason == "" {
				notReadyReason = t.Reason
			}
		}
	}
	switch {
	case failed > 0:
		return capability{State: "BUILD FAILED", Reason: failReason}
	case blocked > 0:
		return capability{State: "UNAVAILABLE", Reason: blockedReason}
	case pass > 0 && notReady > 0:
		return capability{State: "BUILD PASS · NOT READY", Reason: notReadyReason}
	case pass == len(targets):
		return capability{State: "BUILD PASS"}
	case notReady == len(targets):
		return capability{State: "NOT READY", Reason: notReadyReason}
	default:
		return capability{State: "BUILD AVAILABLE"}
	}
}

func capabilityStyle(state string) lipgloss.Style {
	switch {
	case strings.Contains(state, "NOT READY"):
		return styleWarn
	case strings.HasPrefix(state, "BUILD PASS"):
		return styleOK
	case state == "BUILD FAILED", state == "UNAVAILABLE":
		return styleErr
	case state == "BUILD AVAILABLE", state == "BUILDING":
		return styleMuted
	default:
		return styleWarn
	}
}

func (m *model) viewDashboard() string {
	menu := []string{"Translation browser", "Corpus overview", "Build manager"}
	var menuLines []string
	for i, item := range menu {
		if i == m.menuCursor {
			menuLines = append(menuLines, styleSelected.Render("> "+item))
			continue
		}
		menuLines = append(menuLines, "  "+item)
	}
	left := stylePanelFocus.Render("menu\n\n" + strings.Join(menuLines, "\n"))

	card := func(title, value, note string) string {
		return stylePanel.Render(styleAccent.Render(title) + "\n" + value + "\n" + styleMuted.Render(note))
	}
	variantNote := "validated available · no-honorifics only"
	if m.overridesErr != "" {
		variantNote = "validated unavailable (see status)"
	}
	cards := lipgloss.JoinHorizontal(lipgloss.Top,
		card("records", fmt.Sprintf("%d", m.stats.Records), fmt.Sprintf("%d files", m.stats.Files)),
		card("official en", fmt.Sprintf("%d", m.stats.Official), fmt.Sprintf("%d missing", m.stats.MissingOfficial)),
		card("real tl", fmt.Sprintf("%d", m.stats.RealEN), "approved translations"),
		card("variant", fmt.Sprintf("%d", m.overrides), variantNote),
	)
	sw := m.switchCapability()
	pc := m.pcCapability()
	// Loading parses every corpus file and counts its records but does not run corpuspack.Validate, so the
	// dashboard reports the load state only and never a validation pass that was not performed.
	status := styleOK.Render("corpus: LOADED") + styleMuted.Render(" · validation: NOT RUN") + "\n"
	status += styleMuted.Render("switch build: ") + capabilityStyle(sw.State).Render(sw.State) + "\n"
	if sw.Reason != "" {
		status += styleMuted.Render("  reason: "+sw.Reason) + "\n"
	}
	status += styleMuted.Render("switch runtime: ") + styleWarn.Render(build.RuntimeUnknown) + "\n"
	status += styleMuted.Render("pc build: ") + capabilityStyle(pc.State).Render(pc.State) + "\n"
	if pc.Reason != "" {
		status += styleMuted.Render("  reason: "+pc.Reason) + "\n"
	}
	status += styleMuted.Render("pc runtime: ") + styleWarn.Render(build.RuntimeUnknown) + "\n"
	if !m.snapshot.IsZero() {
		status += styleMuted.Render("corpus snapshot "+m.snapshot.Format("2006-01-02 15:04:05")+" · r reload") + "\n"
	}
	if m.overridesErr != "" {
		status += styleErr.Render("variant: "+m.overridesErr) + "\n"
	}
	if m.verErr != "" {
		status += styleErr.Render("version: "+m.verErr) + "\n"
	}
	if m.lastErr != "" {
		status += styleErr.Render(m.lastErr) + "\n"
	}
	right := stylePanel.Render("status\n\n" + status)
	keys := styleMuted.Render("tab sections · enter open · / search · n edition · m layout · r reload · ? help · q quit")
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, left, lipgloss.JoinVertical(lipgloss.Left, cards, right)),
		"", keys)
}

func (m *model) viewBrowser() string {
	var tree strings.Builder
	tree.WriteString(styleAccent.Render("corpus") + "\n")
	limit := m.height - 8
	for i, n := range m.nodes {
		if i > limit && i > m.nodeCursor {
			break
		}
		line := fmt.Sprintf("%-44s %4d files %7d rec", n.label, n.files, n.records)
		if i == m.nodeCursor {
			tree.WriteString(styleSelected.Render("> "+line) + "\n")
			continue
		}
		tree.WriteString("  " + line + "\n")
	}
	left := stylePanelFocus.Render(tree.String())

	header := styleAccent.Render("records") + "  " + styleMuted.Render(m.search.View())
	var list strings.Builder
	start := 0
	if m.recCursor > m.height-12 {
		start = m.recCursor - (m.height - 12)
	}
	for i := start; i < len(m.filtered) && i < start+m.height-10; i++ {
		r := m.records[m.filtered[i]]
		label := derefStr(r.Context.Speaker.LabelJP)
		if label == "" {
			label = "-"
		}
		line := fmt.Sprintf("%-16s %s", truncate(label, 16), truncate(derefStr(r.Text.JP), 40))
		if i == m.recCursor {
			list.WriteString(styleSelected.Render("> "+line) + "\n")
			continue
		}
		list.WriteString("  " + line + "\n")
	}
	right := stylePanel.Render(header + "\n" + list.String() + "\n" + styleMuted.Render("preview") + "\n" + m.preview.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m *model) viewBuild() string {
	m.ensureTable()
	head := styleAccent.Render("Build manager") + "   " + styleMuted.Render("b: Build All   enter: build selection   n/m: filters")
	progressLine := ""
	if m.building {
		progressLine = m.spin.View() + " " + m.stage
	}
	return lipgloss.JoinVertical(lipgloss.Left, head, "", m.tbl.View(), "", progressLine, styleMuted.Render(artifactSummary(m.targets)))
}

func (m *model) ensureTable() {
	if len(m.tbl.Columns()) > 0 {
		return
	}
	cols := []table.Column{
		{Title: "platform", Width: 9},
		{Title: "layout", Width: 12},
		{Title: "edition", Width: 15},
		{Title: "state", Width: 11},
		{Title: "artefact", Width: 42},
	}
	rows := make([]table.Row, 0, len(m.targets))
	for _, t := range m.targets {
		art := "-"
		if t.Artifact != "" {
			art = filepath.Base(t.Artifact)
		}
		rows = append(rows, table.Row{t.Platform, t.Layout, t.Edition, t.State, art})
	}
	m.tbl = table.New(table.WithColumns(cols), table.WithRows(rows), table.WithFocused(true), table.WithHeight(8), table.WithWidth(100))
}

func artifactSummary(targets []build.Target) string {
	if len(targets) == 0 {
		return "no build run yet — press b for Build All"
	}
	pass, other := 0, 0
	for _, t := range targets {
		if t.State == build.StatePass {
			pass++
		} else {
			other++
		}
	}
	return fmt.Sprintf("%d artefacts · %d targets not produced", pass, other)
}

func (m *model) renderHelp() string {
	lines := []string{
		styleTitle.Render("FFRS UNBURGER — keys"),
		"",
		"tab        switch between dashboard, browser and build manager",
		"enter      open the selected file / run the selected build",
		"esc        back to the dashboard",
		"/          search jp, official en, real tl, id, namespace or key",
		"n / m      toggle edition (canonical / no-honorifics) and layout (emulator / atmosphere)",
		"b          Build All (every reachable target)",
		"r          reload corpus snapshot, variant count and version from disk",
		"[ ]        previous / next record in the preview",
		"?          this help",
		"q          quit",
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
