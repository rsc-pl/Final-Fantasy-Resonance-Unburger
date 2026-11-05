package build

// Build targets are a matrix, not six pipelines. One canonical composition is the base of every artifact; an
// approved variant overrides only the lines it names; a layout changes only the directory a payload is mounted
// from. A target that cannot be produced says why and does not stop the others.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unburger/internal/locres"

	"unburger/internal/corpuspack"
	"unburger/internal/variant"
)

// A target is one artefact of the matrix.
type Target struct {
	Platform string `json:"platform"` // switch | pc
	Layout   string `json:"layout"`   // emulator | atmosphere | pc
	Edition  string `json:"edition"`  // canonical | no-honorifics
	Ready    bool   `json:"ready"`
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
	Artifact string `json:"artifact,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

// Edition and layout names.
const (
	EditionCanonical    = "canonical"
	EditionNoHonorifics = "no-honorifics"
	LayoutEmulator      = "emulator"
	LayoutAtmosphere    = "atmosphere"
	LayoutPC            = "pc"
	PlatformSwitch      = "switch"
	PlatformPC          = "pc"
	TitleID             = "0100606026F12000"
	StateNotReady       = "NOT_READY"
)

// Matrix is the ordered list of targets a build tries. Both platforms use the same composition and only the
// mount layout differs; a target that cannot be produced reports its own reason without stopping the rest.
func Matrix() []Target {
	return []Target{
		{Platform: PlatformSwitch, Layout: LayoutEmulator, Edition: EditionCanonical},
		{Platform: PlatformSwitch, Layout: LayoutEmulator, Edition: EditionNoHonorifics},
		{Platform: PlatformSwitch, Layout: LayoutAtmosphere, Edition: EditionCanonical},
		{Platform: PlatformSwitch, Layout: LayoutAtmosphere, Edition: EditionNoHonorifics},
		{Platform: PlatformPC, Layout: LayoutPC, Edition: EditionCanonical},
		{Platform: PlatformPC, Layout: LayoutPC, Edition: EditionNoHonorifics},
	}
}

// AllOptions names the inputs shared by every target.
type AllOptions struct {
	Version   string
	CorpusDir string
	LocresIn  string
	DistDir   string
	WorkDir   string
	Filter    Filter
}

// Filter narrows a run to one platform, layout or edition. An empty field matches everything, so the default
// run builds the whole matrix.
type Filter struct {
	Platform string
	Layout   string
	Edition  string
}

func (f Filter) matches(t Target) bool {
	if f.Platform != "" && f.Platform != t.Platform {
		return false
	}
	if f.Layout != "" && f.Layout != t.Layout {
		return false
	}
	if f.Edition != "" && f.Edition != t.Edition {
		return false
	}
	return true
}

// ArtifactName is the file name of one target. It carries platform, layout and edition so two artefacts can
// never be mistaken for each other.
func ArtifactName(t Target, ver string) string {
	// A PC target has no separate console layout, so naming it twice would only produce "pc-pc".
	parts := []string{"ffrs-en", t.Platform}
	if t.Layout != t.Platform {
		parts = append(parts, t.Layout)
	}
	parts = append(parts, t.Edition)
	if ver != "" {
		parts = append(parts, "v"+ver)
	}
	return strings.Join(parts, "-") + ".zip"
}

// RunAll builds every target the filter selects. A blocked target is reported and the rest continue; the
// returned error is reserved for a failure that makes the whole run meaningless.
func RunAll(ctx context.Context, opt AllOptions, vf *variant.File) ([]Target, error) {
	targets := Matrix()
	idx, err := corpuspack.LoadIndex(opt.CorpusDir)
	if err != nil {
		return nil, err
	}
	// The canonical composition is built once and shared by every target that needs it.
	canonical, cerr := Run(ctx, Options{CorpusDir: opt.CorpusDir, LocresIn: opt.LocresIn, OutDir: filepath.Join(opt.WorkDir, "canonical"), Version: opt.Version})
	out := make([]Target, 0, len(targets))
	for _, t := range targets {
		if !opt.Filter.matches(t) {
			continue
		}
		switch {
		case cerr != nil:
			t.State, t.Reason = StateFailed, cerr.Error()
		case canonical.State != StatePass:
			t.State, t.Reason = canonical.State, canonical.Reason
		case t.Edition == EditionNoHonorifics && (vf == nil || len(vf.Overrides) == 0):
			t.State = StateNotReady
			t.Reason = "no approved no-honorifics overrides: the variant is not a second composition and a copy of the canon may not be published as it"
		default:
			payload := filepath.Join(opt.WorkDir, "canonical")
			if t.Edition == EditionNoHonorifics {
				payload = filepath.Join(opt.WorkDir, "no-honorifics")
				if err := os.MkdirAll(payload, 0o755); err != nil {
					return nil, err
				}
				if err := ComposeVariant(canonical.Output, payload, idx, vf); err != nil {
					t.State, t.Reason = StateFailed, err.Error()
					break
				}
			}
			zipPath, err := PackageLayout(payload, opt.DistDir, t, opt.Version)
			if err != nil {
				t.State, t.Reason = StateFailed, err.Error()
				break
			}
			fi, _ := os.Stat(zipPath)
			t.State, t.Ready, t.Artifact = StatePass, true, zipPath
			if fi != nil {
				t.Size = fi.Size()
			}
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Platform != out[j].Platform {
			return out[i].Platform < out[j].Platform
		}
		if out[i].Layout != out[j].Layout {
			return out[i].Layout < out[j].Layout
		}
		return out[i].Edition < out[j].Edition
	})
	return out, nil
}

// ComposeVariant writes the variant edition of an already composed resource. It starts from the canonical output,
// replaces only the entries an override names, and requires every other entry to stay identical; a variant that
// changes anything else is a failure, not a silent editorial change.
func ComposeVariant(canonicalOutput, payloadDir string, idx *corpuspack.Index, vf *variant.File) error {
	if vf == nil {
		return fmt.Errorf("variant edition requested without a variant file")
	}
	src := canonicalOutput
	if fi, err := os.Stat(src); err == nil && fi.IsDir() {
		// A composition directory holds exactly one resource; naming it directly keeps the caller free to pass
		// either the file or its directory.
		matches, err := filepath.Glob(filepath.Join(src, "*.locres"))
		if err != nil {
			return err
		}
		if len(matches) != 1 {
			return fmt.Errorf("%s holds %d resources, want exactly one", src, len(matches))
		}
		src = matches[0]
	}
	f, err := locres.Read(src)
	if err != nil {
		return err
	}
	replacement := map[string]map[string]string{}
	for _, o := range vf.Overrides {
		rec, ok := idx.ByID[o.RecordID]
		if !ok {
			return fmt.Errorf("override names an unknown record: %s", o.RecordID)
		}
		if replacement[rec.Source.Namespace] == nil {
			replacement[rec.Source.Namespace] = map[string]string{}
		}
		replacement[rec.Source.Namespace][rec.Source.Key] = o.RealEN
	}
	changed := 0
	before := make([]string, len(f.Entries))
	for i := range f.Entries {
		before[i] = f.Entries[i].Text
		e := &f.Entries[i]
		if byKey := replacement[e.Namespace]; byKey != nil {
			if text, ok := byKey[e.Key]; ok {
				e.Text = text
				changed++
			}
		}
	}
	blob, err := locres.Write(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(payloadDir, 0o755); err != nil {
		return err
	}
	out := filepath.Join(payloadDir, filepath.Base(src))
	if err := os.WriteFile(out, blob, 0o644); err != nil {
		return err
	}
	back, err := locres.Read(out)
	if err != nil {
		return fmt.Errorf("variant resource does not parse back: %w", err)
	}
	if len(back.Entries) != len(f.Entries) {
		return fmt.Errorf("variant entry count changed: %d != %d", len(back.Entries), len(f.Entries))
	}
	for i := range f.Entries {
		if back.Entries[i] != f.Entries[i] {
			return fmt.Errorf("variant roundtrip changed %s/%s", f.Entries[i].Namespace, f.Entries[i].Key)
		}
	}
	if changed != len(vf.Overrides) {
		return fmt.Errorf("%d overrides applied, %d declared", changed, len(vf.Overrides))
	}
	return nil
}
