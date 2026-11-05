package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unburger/internal/build"
	"unburger/internal/cache"
	"unburger/internal/corpus"
	"unburger/internal/corpuspack"
	"unburger/internal/discovery"
	"unburger/internal/extract"
	"unburger/internal/hygiene"
	"unburger/internal/ingest"
	"unburger/internal/oodle"
	"unburger/internal/project"
	"unburger/internal/usmap"
	"unburger/internal/variant"
	"unburger/internal/version"
	"unburger/internal/zen"
)

func main() { os.Exit(run(os.Args[1:])) }

// oneline flattens CR/LF so a record prints as one terminal line.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func speakerLabel(r corpuspack.Record) string {
	sp := r.Context.Speaker
	if sp.LabelJP != nil {
		return *sp.LabelJP
	}
	if sp.Applicability == corpuspack.ApplicabilityNotApplicable {
		return "-"
	}
	return "UNKNOWN"
}

func oneline(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " / ")
	s = strings.ReplaceAll(s, "\n", " / ")
	s = strings.ReplaceAll(s, "\r", " / ")
	return s
}

// releaseStatus names the release gate without calling the build itself unsafe. ReadyForBuild includes the
// runtime PASS, so its false value says the project is not release-ready; it does not withhold structural test
// builds, which the build command intentionally leaves ungated on runtime verification.
func releaseStatus(c project.Config) string {
	if c.ReadyForBuild() == nil {
		return "release ready"
	}
	return fmt.Sprintf("not release-ready (source=%v adapter=%v safe_to_patch=%v runtime=%v approved=%v)",
		c.Status.SourceVerified, c.Status.AdapterImplemented, c.Status.SafeToPatch,
		c.Status.RuntimePass, c.Status.ReleaseApproved)
}

func run(args []string) int {
	if len(args) == 0 {
		args = []string{"status"}
	}
	root, e := os.Getwd()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 2
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(`Usage: unburger <command> [arguments]

  status | doctor | discover | cache [verify] | extract | ingest | thread [scene]
  corpus validate|build|export|import|search|scenes
  locres fetch | datatables | talkevents | charanames | cuesheets | identity
  schemarows | talkrows | functions | families | audit | glossary | untranslated
  provenance | migrate-unreviewed
  verify-pak | patch-pak | verify-mod | testmod
  build|package [--locres <path>] [--platform switch|pc]
                 [--layout emulator|atmosphere] [--edition canonical|no-honorifics]
  clean [--apply] | tui | help

build and package run the same composition and packaging path; "package" is kept as a compatible alias.
`)
		return 0
	}
	if args[0] == "clean" {
		apply := len(args) == 2 && args[1] == "--apply"
		if len(args) > 2 || (len(args) == 2 && !apply) {
			return 2
		}
		paths, e := cache.Cleanup(root, apply)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		for _, p := range paths {
			rel, _ := filepath.Rel(root, p)
			fmt.Println(rel)
		}
		fmt.Printf("%d eligible transient files (applied=%v)\n", len(paths), apply)
		return 0
	}
	c, e := project.Load(root)
	if e != nil {
		fmt.Fprintln(os.Stderr, "CONFIG ERROR:", e)
		return 2
	}
	switch args[0] {
	case "status":
		fmt.Printf("%s | canonical=%s | targets=switch,pc | source=%s→%s | voice=%s (verified=%v)\n", c.Title, c.CanonicalPlatform, c.SourceLanguage, c.TargetLanguage, c.Audio.PreferredVoiceLanguage, c.Audio.VoicePatchVerified)
		counts, e := corpus.Validate(root)
		if e != nil {
			fmt.Println("CORPUS ERROR:", e)
		} else {
			fmt.Printf("Corpus: JP=%d official EN=%d canonical EN=%d\n", counts["jp"], counts["loc"], counts["en"])
		}
		fmt.Printf("Found %d source installations. %s; structural test builds are not gated on runtime verification\n", len(discovery.Find(root, c)), releaseStatus(c))
		return 0
	case "tui":
		return runTUI(root)
	case "locres":
		// locres fetch [--out <dir>]: walk the physical .pak entries, recognise archives by signature.
		if len(args) < 2 || args[1] != "fetch" {
			fmt.Fprintln(os.Stderr, "usage: unburger locres fetch [--out <dir>]")
			return 2
		}
		out := filepath.Join(root, ".local", "work", "locres")
		for i := 2; i+1 < len(args); i++ {
			if args[i] == "--out" {
				out = args[i+1]
			}
		}
		containers := []extract.Container{}
		for _, c := range []struct{ rel, layer string }{
			{".local/originals/switch/romfs/FFRS/Content/Paks/FFRS-Switch.pak", "base"},
			{".local/originals/switch/updates/1.0.1/romfs/FFRS/Content/Paks/FFRS-Switch.pak", "update-1.0.1"},
		} {
			p := filepath.Join(root, filepath.FromSlash(c.rel))
			if _, err := os.Stat(p); err == nil {
				containers = append(containers, extract.Container{Path: p, Layer: c.layer})
			}
		}
		rep, e := extract.FetchAllLocres(root, containers, out)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		for _, a := range rep.Archives {
			fmt.Printf("  %s %-16s %-12s entries=%-6d ns=%-4d %v\n", a.Short, a.Layer, a.Container, a.Entries, a.Namespaces, a.Sample)
		}
		fmt.Printf("containers=%d scanned=%d localisation_entries=%d distinct=%d\n", rep.Containers, rep.Scanned, rep.Found, rep.Distinct)
		if rep.Reason != "" {
			fmt.Println("reason:", rep.Reason)
		}
		if err := extract.WriteFetchReport(filepath.Join(root, ".local", "work", "locres", "fetch-report.json"), rep); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if rep.Distinct == 0 {
			return 2
		}
		return 0
	case "verify-pak":
		return runVerifyPak(args[1:], os.Stdout, os.Stderr)
	case "patch-pak":
		return runPatchPak(args[1:], os.Stdout, os.Stderr)
	case "testmod":
		// testmod <namespace> <key> <text>: one controlled line, private output only, never the canon.
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, "usage: unburger testmod <namespace> <key> <replacement text>")
			return 2
		}
		idx, e := corpuspack.LoadIndex(filepath.Join(root, "translations"))
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		ver, verr := version.Load(filepath.Join(root, version.FileName))
		if verr != nil {
			fmt.Fprintln(os.Stderr, "VERSION FAIL:", verr)
			return 2
		}
		var tc []build.TestChange
		for i := 1; i+2 < len(args); i += 3 {
			tc = append(tc, build.TestChange{Namespace: args[i], Key: args[i+1], Text: args[i+2]})
		}
		work := filepath.Join(root, ".local", "testmod", "payload")
		infos, e := build.ComposeArchivesWith(idx, filepath.Join(root, ".local", "work", "locres"), work, tc, ver)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		changed := 0
		for _, i := range infos {
			if i.Changed > 0 {
				changed += i.Changed
				fmt.Printf("  %s %s changed=%d\n", i.File, i.Language, i.Changed)
			}
		}
		if changed == 0 {
			fmt.Println("no archive contains that namespace/key: nothing to test")
			return 2
		}
		// A test artefact may carry the structurally verified package-free container; its runtime state stays
		// UNVERIFIED and it is never treated as an approved release.
		build.AllowEmptyContainer = true
		target, e := build.PackageLayout(work, filepath.Join(root, ".local", "testmod", "dist"), build.Target{
			Platform: build.PlatformSwitch, Layout: build.LayoutEmulator, Edition: "test-change", Ready: true,
		}, ver)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Println("test artefact:", target)
		return 0
	case "verify-mod":
		// verify-mod <zip|dir>: check the mount layout a console or emulator will actually see.
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: unburger verify-mod <zip|dir>")
			return 2
		}
		res, e := inspectMod(args[1])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Print(res)
		return 0
	case "doctor":
		fails := hygiene.Run(root)
		if _, e = corpuspack.Validate(filepath.Join(root, "translations")); e != nil {
			fails = append(fails, e)
		}
		for _, x := range fails {
			fmt.Fprintln(os.Stderr, "FAIL:", x)
		}
		if len(fails) > 0 {
			return 1
		}
		fmt.Println("PASS: file hygiene, authority, structure and corpus schema. Game writer remains unverified.")
		return 0
	case "discover", "cache":
		if len(args) > 2 || (len(args) == 2 && (args[0] != "cache" || args[1] != "verify")) {
			return 2
		}
		found := discovery.Find(root, c)
		if len(found) == 0 {
			fmt.Println("No supported source containers found; no paths prompted.")
			return 0
		}
		for _, s := range found {
			fmt.Printf("%s: %s (%d containers)\n", s.Platform, s.ContainerDir, len(s.Files))
			if args[0] == "cache" {
				m, h, e := cache.Index(root, s, len(args) == 2)
				if e != nil {
					fmt.Fprintln(os.Stderr, e)
					return 1
				}
				fmt.Printf("Index: %d files; %d cached hashes. Raw text extraction requires a verified format adapter.\n", len(m.Files), h)
			}
		}
		return 0
	case "extract":
		if len(args) != 1 {
			return 2
		}
		count, e := extract.Loose(root)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Printf("Loose locres files: %d\n", count)
		paked, skipped, e := extract.Packed(root, discovery.Find(root, c))
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Printf("Packed locres archives: %d (compressed entries skipped: %d)\n", paked, skipped)
		if skipped > 0 {
			fmt.Println("NO DECODER: place the Oodle library under .local/toolchain/vendor/oodle/ to read compressed archives.")
			return 1
		}
		return 0
	case "ingest":
		if len(args) != 1 {
			return 2
		}
		report, e := ingest.Run(root)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Print(report.String())
		return 0
	case "thread":
		idx, e := corpuspack.LoadIndex(filepath.Join(root, "translations"))
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		if len(args) == 1 {
			names := make([]string, 0, len(idx.ByNS))
			for ns := range idx.ByNS {
				names = append(names, ns)
			}
			sort.Strings(names)
			for _, ns := range names {
				fmt.Printf("%-44s %5d lines\n", ns, len(idx.ByNS[ns]))
			}
			fmt.Printf("%d namespaces, %d records\n", len(names), idx.Stats.Records)
			return 0
		}
		if len(args) != 2 {
			return 2
		}
		recs, ok := idx.ByNS[args[1]]
		if !ok {
			fmt.Fprintln(os.Stderr, "no such scene namespace:", args[1])
			return 1
		}
		for i, r := range recs {
			fmt.Printf("[%04d] %s\n  JP  %s\n  EN  %s\n", i+1, speakerLabel(r), oneline(deref(r.Text.JP)), oneline(deref(r.Text.OfficialEN)))
		}
		fmt.Printf("%d lines\n", len(recs))
		return 0
	case "corpus":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: unburger corpus validate|build|export|import|search|scenes ...")
			return 2
		}
		switch args[1] {
		case "validate":
			dir := filepath.Join(root, "translations")
			if len(args) > 2 {
				dir = args[2]
			}
			st, e := corpuspack.Validate(dir)
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
			fmt.Printf("PASS canonical corpus: files=%d records=%d jp=%d official_en=%d real_en=%d missing_jp=%d missing_official_en=%d\n",
				st.Files, st.Records, st.JP, st.Official, st.RealEN, st.MissingJP, st.MissingOfficial)
			for _, k := range sortedKeys(st.ByReview) {
				fmt.Printf("  review %-22s %d\n", k, st.ByReview[k])
			}
			return 0
		case "build":
			// corpus build <jp> <loc> <keys> <out-dir> [--existing <dir>]
			if len(args) < 6 {
				fmt.Fprintln(os.Stderr, "usage: unburger corpus build <jp.json> <loc.json> <key-identities.json> <out-dir> [--existing <dir>]")
				return 2
			}
			opt := corpuspack.BuildOptions{JP: args[2], Loc: args[3], Keys: args[4], OutDir: args[5], RealTL: filepath.Join(root, "translations", "en", "records.json")}
			for i := 6; i+1 < len(args); i++ {
				if args[i] == "--existing" {
					opt.Existing = args[i+1]
				}
			}
			bindings, e := zen.LoadBindings(opt.Keys)
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
			ns := map[string]bool{}
			for _, b := range bindings {
				if b.Namespace != "" {
					ns[b.Namespace] = true
				}
			}
			corpuspack.SetDialogueNamespaces(ns)
			res, e := corpuspack.Build(opt)
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
			fmt.Printf("built: files=%d records=%d jp=%d official_en=%d real_en=%d missing_jp=%d missing_official_en=%d\n",
				res.Manifest.Counts.Files, res.Manifest.Counts.Records, res.Manifest.Counts.JP,
				res.Manifest.Counts.OfficialEN, res.Manifest.Counts.RealEN,
				res.Manifest.Counts.MissingJP, res.Manifest.Counts.MissingOfficialEN)
			return 0
		case "export":
			// corpus export <out.json> <namespace>...
			if len(args) < 4 {
				fmt.Fprintln(os.Stderr, "usage: unburger corpus export <out.json> <namespace>...")
				return 2
			}
			f, e := corpuspack.Export(corpuspack.ExportOptions{Dir: filepath.Join(root, "translations"), Namespace: args[3:], Out: args[2]})
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
			fmt.Printf("exported %d records to %s\n", len(f.Records), args[2])
			return 0
		case "import":
			// corpus import <patch.json> [--dry-run] [--history <dir>]
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: unburger corpus import <patch.json> [--dry-run] [--history <dir>]")
				return 2
			}
			opt := corpuspack.ImportOptions{Dir: filepath.Join(root, "translations"), Patch: args[2], History: filepath.Join(root, ".local", "history")}
			for i := 3; i < len(args); i++ {
				switch args[i] {
				case "--dry-run":
					opt.DryRun = true
				case "--history":
					if i+1 < len(args) {
						opt.History = args[i+1]
					}
				}
			}
			res, e := corpuspack.Import(opt)
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
			mode := "applied"
			if opt.DryRun {
				mode = "dry-run"
			}
			fmt.Printf("%s: records=%d applied=%d skipped=%d files=%d approval=%s\n", mode, res.Records, res.Applied, res.Skipped, len(res.Files), res.Approval)
			return 0
		case "search":
			if len(args) < 4 {
				fmt.Fprintln(os.Stderr, "usage: unburger corpus search <text>")
				return 2
			}
			idx, e := corpuspack.LoadIndex(filepath.Join(root, "translations"))
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
			q := strings.ToLower(args[2])
			hits := 0
			for _, id := range idx.Order {
				r := idx.ByID[id]
				if strings.Contains(strings.ToLower(deref(r.Text.JP)), q) || strings.Contains(strings.ToLower(deref(r.Text.OfficialEN)), q) {
					fmt.Printf("%s\n  JP %s\n  EN %s\n", r.RecordID, oneline(deref(r.Text.JP)), oneline(deref(r.Text.OfficialEN)))
					hits++
					if hits >= 50 {
						break
					}
				}
			}
			fmt.Printf("%d hits\n", hits)
			return 0
		case "scenes":
			idx, e := corpuspack.LoadIndex(filepath.Join(root, "translations"))
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
			names := make([]string, 0, len(idx.ByNS))
			for ns := range idx.ByNS {
				names = append(names, ns)
			}
			sort.Strings(names)
			dialogue := 0
			for _, ns := range names {
				for _, r := range idx.ByNS[ns] {
					if r.Function == "dialogue_line" {
						fmt.Printf("%-44s %5d lines\n", ns, len(idx.ByNS[ns]))
						dialogue++
						break
					}
				}
			}
			fmt.Printf("%d dialogue namespaces\n", dialogue)
			return 0
		default:
			fmt.Fprintln(os.Stderr, "unknown corpus command:", args[1])
			return 2
		}
	case "datatables":
		if len(args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: unburger datatables <paks-dir> <out.json>")
			return 2
		}
		lib := filepath.Join(root, ".local/toolchain/vendor/oodle/liboo2corelinux64.so.9")
		if e := oodle.Load(lib); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		decode := func(c []byte, n int) ([]byte, error) { return oodle.Decode(lib, c, n) }
		tables, e := zen.Inventory(args[1], decode)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		if e := zen.WriteInventory(args[2], tables); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		counts := map[string]int{}
		for _, t := range tables {
			counts[t.RowStruct]++
		}
		type kv struct {
			name string
			n    int
		}
		var list []kv
		for k, v := range counts {
			list = append(list, kv{k, v})
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].n != list[j].n {
				return list[i].n > list[j].n
			}
			return list[i].name < list[j].name
		})
		fmt.Printf("DataTables: %d\n", len(tables))
		for i, e := range list {
			if i >= 12 {
				break
			}
			fmt.Printf("  %5d  %s\n", e.n, e.name)
		}
		return 0
	case "talkevents":
		if len(args) != 4 {
			fmt.Fprintln(os.Stderr, "usage: unburger talkevents <paks-dir> <usmap> <out.json>")
			return 2
		}
		lib := filepath.Join(root, ".local/toolchain/vendor/oodle/liboo2corelinux64.so.9")
		if e := oodle.Load(lib); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		m, e := usmap.Load(args[2])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		decode := func(c []byte, n int) ([]byte, error) { return oodle.Decode(lib, c, n) }
		res, e := zen.CollectDialogueRows(args[1], decode, m)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		if e := zen.WriteDialogueRows(args[3], res); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Printf("dialogue tables=%d tiled=%d rows=%d rows_with_key=%d items=%d\n",
			res.Tables, res.Tiled, res.Rows, res.WithKey, len(res.Items))
		return 0
	case "charanames":
		if len(args) != 4 {
			fmt.Fprintln(os.Stderr, "usage: unburger charanames <jp-records.json> <speakers.json> <out.json>")
			return 2
		}
		idx, e := zen.LoadCharNames(args[1])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		labels, e := zen.SpeakerLabels(args[2])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		rep := zen.BuildIdentityReport(idx, labels)
		if e := zen.WriteIdentityReport(args[3], rep); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Printf("chara entries=%d labels=%d unique=%d ambiguous=%d missing=%d keys=%d/%d=%.1f%%\n",
			rep.Entries, rep.Labels, rep.Unique, rep.Ambiguous, rep.Missing,
			rep.KeysWithIdentity, rep.KeysTotal, rep.CoveragePercent)
		return 0
	case "provenance":
		idx, e := corpuspack.LoadIndex(filepath.Join(root, "translations"))
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		reviewed, withReason := 0, 0
		for _, r := range idx.ByID {
			if r.Review.Status != corpuspack.StatusUnreviewed {
				reviewed++
				if r.Review.Reason != nil && *r.Review.Reason != "" {
					withReason++
				}
			}
		}
		fmt.Printf("records=%d unreviewed=%d reviewed=%d with_reason=%d real_en=%d\n",
			idx.Stats.Records, idx.Stats.ByReview[corpuspack.StatusUnreviewed], reviewed, withReason, idx.Stats.RealEN)
		for _, k := range sortedKeys(idx.Stats.ByReview) {
			fmt.Printf("  review %-22s %d\n", k, idx.Stats.ByReview[k])
		}
		fmt.Printf("  files=%d types=%d\n", idx.Stats.Files, len(idx.Stats.ByType))
		return 0
	case "migrate-unreviewed":
		if len(args) != 5 {
			fmt.Fprintln(os.Stderr, "usage: unburger migrate-unreviewed <en-records.json> <official-en.json> <history.json> <out.json>")
			return 2
		}
		rep, e := zen.MigrateToUnreviewed(args[1], args[2], args[3], args[4])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Printf("migrated: records=%d approved=%d moved_to_history=%d\n", rep.Records, rep.Approved, rep.Unapproved)
		return 0
	case "glossary":
		if len(args) != 4 {
			fmt.Fprintln(os.Stderr, "usage: unburger glossary <jp-records.json> <loc-records.json> <out.json>")
			return 2
		}
		g, e := zen.BuildGlossary(args[1], args[2], 3)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		if e := zen.WriteGlossary(args[3], g); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Printf("entries=%d unresolved=%d\n", len(g.Entries), len(g.Unresolved))
		for _, x := range g.Entries {
			fmt.Printf("  %-8s %-12s -> %-16s evidence=%5d %s\n", x.Field, x.JP, x.EN, x.Evidence, x.Status)
		}
		for _, x := range g.Unresolved {
			fmt.Printf("  UNRESOLVED %-10s -> %-14s evidence=%d\n", x.JP, x.EN, x.Evidence)
		}
		return 0
	case "untranslated":
		if len(args) != 4 {
			fmt.Fprintln(os.Stderr, "usage: unburger untranslated <jp-records.json> <loc-records.json> <out.json>")
			return 2
		}
		bindings, e := zen.LoadBindings(filepath.Join(root, ".local/toolchain/records/key-identities.json"))
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		dialogue := map[string]bool{}
		for _, b := range bindings {
			if b.Namespace != "" {
				dialogue[b.Namespace] = true
			}
		}
		rep, e := zen.FindUntranslated(args[1], args[2], dialogue)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		if e := zen.WriteUntranslatedReport(args[3], rep); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Printf("aligned=%d untranslated=%d (%.1f%%) namespaces=%d\n",
			rep.Aligned, rep.Untranslated, rep.Percent, len(rep.ByNamespace))
		for i, c := range rep.ByNamespace {
			if i == 12 {
				break
			}
			fmt.Printf("  %-32s %5d  %s\n", c.Namespace, c.Records, c.Function)
		}
		return 0
	case "families":
		if len(args) != 4 {
			fmt.Fprintln(os.Stderr, "usage: unburger families <jp-records.json> <usmap> <out.json>")
			return 2
		}
		ns, e := zen.CorpusNamespaces(args[1])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		m, e := usmap.Load(args[2])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		engine := map[string]bool{}
		for _, n := range m.Names {
			engine[n] = true
		}
		bindings, e := zen.LoadBindings(filepath.Join(root, ".local/toolchain/records/key-identities.json"))
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		dialogueNS := map[string]bool{}
		for _, b := range bindings {
			if b.Namespace != "" {
				dialogueNS[b.Namespace] = true
			}
		}
		rep := zen.BuildFamilyReport(ns, dialogueNS, engine)
		if e := zen.WriteFamilyReport(args[3], rep); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Printf("records=%d namespaces=%d\n", rep.Records, len(ns))
		for _, c := range rep.Classes {
			fmt.Printf("  %-22s %5d namespaces %7d records  speaker=%s\n", c.Class, c.Namespaces, c.Records, c.Speaker)
		}
		return 0
	case "audit":
		if len(args) != 5 {
			fmt.Fprintln(os.Stderr, "usage: unburger audit <batch.json> <jp-records.json> <loc-records.json> <out.json>")
			return 2
		}
		batch, e := zen.LoadAuditBatch(args[1])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		recs, e := zen.ApplyAudit(batch, args[2], args[3])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		if e := zen.WriteAuditedRecords(args[4], recs); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		counts := map[string]int{}
		for _, r := range recs {
			counts[r.Status]++
		}
		fmt.Printf("pass=%s family=%s records=%d KEEP=%d CHANGE=%d HOLD=%d UNREVIEWED=%d\n",
			batch.Pass, batch.Family, len(recs), counts["KEEP"], counts["CHANGE"], counts["HOLD"], counts["UNREVIEWED"])
		return 0
	case "functions":
		if len(args) != 4 {
			fmt.Fprintln(os.Stderr, "usage: unburger functions <jp-records.json> <key-identities.json> <out.json>")
			return 2
		}
		sets, e := zen.NamespaceKeySets(args[1])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		bindings, e := zen.LoadBindings(args[2])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		rep := zen.BuildFunctionReport(sets, bindings)
		if e := zen.WriteFunctionReport(args[3], rep); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Printf("records=%d applicable=%d verified=%d unknown=%d not_applicable=%d coverage=%.1f%% named_entry=%.1f%%\n",
			rep.Records, rep.Applicable, rep.Verified, rep.Unknown, rep.NotApplicable, rep.CoveragePct, rep.NamedEntryPct)
		for _, f := range rep.Functions {
			fmt.Printf("  %-18s %6d  verified=%6d unknown=%5d n/a=%6d\n", f.Function, f.Records, f.Verified, f.Unknown, f.NotAppl)
		}
		return 0
	case "cuesheets":
		if len(args) != 4 {
			fmt.Fprintln(os.Stderr, "usage: unburger cuesheets <paks-dir> <usmap> <out.json>")
			return 2
		}
		lib := filepath.Join(root, ".local/toolchain/vendor/oodle/liboo2corelinux64.so.9")
		if e := oodle.Load(lib); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		m, e := usmap.Load(args[2])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		decode := func(c []byte, n int) ([]byte, error) { return oodle.Decode(lib, c, n) }
		rec, e := zen.CollectCueSheets(args[1], decode, m)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		if e := zen.WriteCueSheets(args[3], rec); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Printf("cue-sheet tables=%d rows=%d\n", len(rec.Tables), rec.RowTotal)
		return 0
	case "identity":
		if len(args) != 5 {
			fmt.Fprintln(os.Stderr, "usage: unburger identity <dialogue-rows.json> <character-names.json> <jp-records.json> <out.json>")
			return 2
		}
		rows, e := zen.LoadDialogueRows(args[1])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		ids, e := zen.LoadIdentities(args[2])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		sets, e := zen.NamespaceKeySets(args[3])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		rep := zen.BuildKeyIdentities(rows, ids, sets)
		if e := zen.WriteKeyIdentities(args[4], rep); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Printf("entries=%d labelled=%d identified=%d ambiguous=%d missing=%d markers=%d conflicts=%d unresolved_ns=%d\n",
			rep.DialogueKeys, rep.KeysWithLabel, rep.KeysIdentified, rep.KeysAmbiguous,
			rep.KeysMissing, rep.KeysMarker, rep.KeyConflicts, rep.KeysUnresolvedNamespace)
		fmt.Printf("coverage label=%.1f%% id-of-labelled=%.1f%% id-of-corpus=%.1f%% pairs=%d outside=%d\n",
			rep.CoverageByLabelPct, rep.CoverageByIDAssigned, rep.CoverageByIDCorpus,
			rep.PairsTotal, rep.PairsOutsideKeys)
		return 0
	case "schemarows":
		if len(args) < 3 || len(args) > 4 {
			fmt.Fprintln(os.Stderr, "usage: unburger schemarows <paks-dir> <usmap> [failures.json]")
			return 2
		}
		lib := filepath.Join(root, ".local/toolchain/vendor/oodle/liboo2corelinux64.so.9")
		if e := oodle.Load(lib); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		m, e := usmap.Load(args[2])
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		decode := func(c []byte, n int) ([]byte, error) { return oodle.Decode(lib, c, n) }
		res, e := zen.CollectSchemaTables(args[1], decode, m)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		tiled, rows := 0, 0
		for _, r := range res {
			if r.Tiled() {
				tiled++
				rows += r.Rows
			}
		}
		fmt.Printf("tables=%d tiled=%d rows=%d\n", len(res), tiled, rows)
		for _, line := range zen.SummariseSchemaResults(res) {
			fmt.Println(line)
		}
		if len(args) > 3 {
			if e := zen.WriteSchemaResults(args[3], res); e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
		}
		return 0
	case "talkrows":
		if len(args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: unburger talkrows <paks-dir> <out.json>")
			return 2
		}
		lib := filepath.Join(root, ".local/toolchain/vendor/oodle/liboo2corelinux64.so.9")
		if e := oodle.Load(lib); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		decode := func(c []byte, n int) ([]byte, error) { return oodle.Decode(lib, c, n) }
		res, e := zen.CollectTalkLabels(args[1], decode)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		if e := zen.WriteLabels(args[2], res); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		fmt.Printf("DataTables walked: %d | row sets parsed: %d | rows: %d | key+label pairs: %d | refused: %d\n",
			res.Tables, res.Parsed, res.Rows, len(res.Items), res.Tables-res.Parsed)
		for i, f := range res.Failed {
			if i >= 5 {
				break
			}
			fmt.Println("  refused:", f)
		}
		return 0
	case "build", "package":
		// build and package share this path. A structurally valid test build is permitted regardless of runtime
		// approval; the artefact metadata reports UNVERIFIED until a real runtime test exists, and nothing here
		// marks a build as an approved release.
		ver, verr := version.Load(filepath.Join(root, version.FileName))
		if verr != nil {
			fmt.Fprintln(os.Stderr, "VERSION FAIL:", verr)
			return 2
		}
		opt := build.AllOptions{
			Version:   ver,
			CorpusDir: filepath.Join(root, "translations"),
			LocresIn:  filepath.Join(root, ".local", "work", "locres"),
			DistDir:   filepath.Join(root, "dist"),
			WorkDir:   filepath.Join(root, ".local", "out"),
		}
		for i := 1; i+1 < len(args); i++ {
			switch args[i] {
			case "--locres":
				opt.LocresIn = args[i+1]
			case "--platform":
				opt.Filter.Platform = args[i+1]
			case "--layout":
				opt.Filter.Layout = args[i+1]
			case "--edition":
				opt.Filter.Edition = args[i+1]
			}
		}
		var vf *variant.File
		if v, e := variant.Load(variant.Path(filepath.Join(root, "translations"), variant.NoHonorifics)); e == nil {
			vf = &v
		}
		targets, e := build.RunAll(context.Background(), opt, vf)
		if e != nil {
			fmt.Fprintln(os.Stderr, "BUILD REFUSED:", e)
			return 2
		}
		fail := 0
		for _, t := range targets {
			fmt.Printf("%-7s %-11s %-14s %-8s", t.Platform, t.Layout, t.Edition, t.State)
			if t.Artifact != "" {
				fmt.Printf(" %s (%d bytes)", filepath.Base(t.Artifact), t.Size)
			}
			if t.Reason != "" {
				fmt.Printf(" — %s", t.Reason)
			}
			fmt.Println()
			if t.State != build.StatePass {
				fail++
			}
		}
		fmt.Printf("%d targets, %d artefacts, %d not produced\n", len(targets), len(targets)-fail, fail)
		if fail == len(targets) {
			return 2
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "Unknown command:", strings.Join(args, " "))
		return 2
	}
}
