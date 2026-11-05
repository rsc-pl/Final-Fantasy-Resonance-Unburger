package discovery

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unburger/internal/project"
)

type Source struct {
	Platform     string   `json:"platform"`
	Root         string   `json:"root"`
	ContainerDir string   `json:"container_dir"`
	Files        []string `json:"files"`
}

var quoted = regexp.MustCompile(`"([^"]+)"\s+"([^"]+)"`)

func steamApps(home string) []string {
	roots := []string{filepath.Join(home, ".steam", "steam", "steamapps"), filepath.Join(home, ".steam", "root", "steamapps"), filepath.Join(home, ".local", "share", "Steam", "steamapps")}
	for _, r := range append([]string(nil), roots...) {
		b, e := os.ReadFile(filepath.Join(r, "libraryfolders.vdf"))
		if e != nil {
			continue
		}
		for _, m := range quoted.FindAllStringSubmatch(string(b), -1) {
			if m[1] == "path" {
				roots = append(roots, filepath.Join(strings.ReplaceAll(m[2], `\\`, `\`), "steamapps"))
			}
		}
	}
	return unique(roots)
}
func unique(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = filepath.Clean(s)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
func installedDir(apps, appID string) string {
	b, e := os.ReadFile(filepath.Join(apps, "appmanifest_"+appID+".acf"))
	if e != nil {
		return ""
	}
	for _, m := range quoted.FindAllStringSubmatch(string(b), -1) {
		if m[1] == "installdir" && m[2] != "" && filepath.Base(m[2]) == m[2] {
			return filepath.Join(apps, "common", m[2])
		}
	}
	return ""
}
func Find(root string, c project.Config) []Source {
	options := map[string][]string{}
	for name, p := range c.Platforms {
		for _, r := range p.PreferredPaths {
			options[name] = append(options[name], filepath.Join(root, r))
		}
	}
	if home, e := os.UserHomeDir(); e == nil {
		for _, apps := range steamApps(home) {
			if d := installedDir(apps, c.SteamAppID); d != "" {
				options["pc"] = append(options["pc"], d)
			}
		}
	}
	out := []Source{}
	seen := map[string]bool{}
	for _, platform := range []string{"switch", "pc"} {
		for _, r := range unique(options[platform]) {
			if filepath.IsAbs(r) == false {
				continue
			}
			resolved, e := filepath.EvalSymlinks(r)
			if e != nil {
				continue
			}
			for _, p := range []string{filepath.Join(resolved, "FFRS", "Content", "Paks"), filepath.Join(resolved, "Content", "Paks"), resolved} {
				if seen[platform+":"+p] {
					continue
				}
				seen[platform+":"+p] = true
				ents, e := os.ReadDir(p)
				if e != nil {
					continue
				}
				files := []string{}
				for _, ent := range ents {
					if !ent.Type().IsRegular() {
						continue
					}
					ext := strings.ToLower(filepath.Ext(ent.Name()))
					if ext == ".pak" || ext == ".utoc" || ext == ".ucas" {
						files = append(files, ent.Name())
					}
				}
				if len(files) == 0 {
					continue
				}
				sort.Strings(files)
				out = append(out, Source{Platform: platform, Root: resolved, ContainerDir: p, Files: files})
				break
			}
		}
	}
	return out
}
