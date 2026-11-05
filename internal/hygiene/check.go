package hygiene

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// workspaceSpecPath is the private, Git-ignored inventory of workspace checks. Keeping the document names and
// directory list there means the public scanner stays generic, while a workspace that has the file gets the same
// checks it always had. A public checkout without the file runs the public-tree scan only.
const workspaceSpecPath = ".local/authority/hygiene.json"

type workspaceSpec struct {
	Authority         []string `json:"authority"`
	BaselineDocs      int      `json:"baseline_docs"`
	BaselineRequired  []string `json:"baseline_required"`
	Ledgers           []string `json:"ledgers"`
	Directories       []string `json:"directories"`
	ForbiddenPatterns []string `json:"forbidden_patterns"`
}

// forbidden is the general public-tree check. Real game text legitimately contains codec names and three-digit
// identifiers, so the stricter private patterns are applied to code and configuration only.
var forbidden = regexp.MustCompile(`(?i)(?:/home/[a-z0-9_\-]+/|internal` + ` devlog|personal` + ` workspace|private` + ` provenance)`)

// forbiddenData is the subset applied to translation records: only markers that can never be authentic content.
var forbiddenData = regexp.MustCompile(`(?i)(?:/home/[a-z0-9_\-]+/|internal` + ` devlog|personal` + ` workspace|private` + ` provenance)`)

// identifierExposed reports whether data leaks a private path or a configured private identifier. The extra
// patterns come from the private workspace manifest; public clones have none.
func identifierExposed(rel string, data []byte, extra []*regexp.Regexp) bool {
	if strings.HasPrefix(rel, "translations/") {
		return forbiddenData.Match(data)
	}
	if forbidden.Match(data) {
		return true
	}
	for _, re := range extra {
		if re.Match(data) {
			return true
		}
	}
	return false
}

func loadWorkspaceSpec(root string) (*workspaceSpec, []*regexp.Regexp, []error) {
	var errs []error
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(workspaceSpecPath)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, []error{fmt.Errorf("workspace checks: %v", err)}
	}
	var spec workspaceSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, nil, []error{fmt.Errorf("workspace checks: %v", err)}
	}
	var extra []*regexp.Regexp
	for _, pattern := range spec.ForbiddenPatterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			errs = append(errs, fmt.Errorf("workspace checks: invalid pattern %q: %v", pattern, err))
			continue
		}
		extra = append(extra, re)
	}
	return &spec, extra, errs
}

// gitStderr extracts the diagnostic text Git wrote to stderr, falling back to the error itself.
func gitStderr(err error) string {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return strings.TrimSpace(string(exitErr.Stderr))
	}
	return strings.TrimSpace(err.Error())
}

// gitRootEntries reports the top-level names in the publishable set of a Git work tree. The set is
// `git ls-files --cached --others --exclude-standard`, so tracked paths are always present even when an
// ignore rule matches them, while untracked paths excluded by .gitignore or .git/info/exclude are absent.
// inWorkTree is false when root is not inside a Git work tree; a Git invocation failure is returned as an
// error so the caller never treats it as an empty publishable set.
func gitRootEntries(root string) (entries []string, inWorkTree bool, err error) {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree")
	out, cmdErr := cmd.Output()
	if cmdErr != nil {
		msg := gitStderr(cmdErr)
		if strings.Contains(msg, "not a git repository") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("git root inventory: %v: %s", cmdErr, msg)
	}
	if strings.TrimSpace(string(out)) != "true" {
		return nil, false, nil
	}
	cmd = exec.Command("git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	out, cmdErr = cmd.Output()
	if cmdErr != nil {
		return nil, false, fmt.Errorf("git root inventory: %v: %s", cmdErr, gitStderr(cmdErr))
	}
	seen := make(map[string]bool)
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		name := p
		if i := strings.IndexByte(p, '/'); i >= 0 {
			name = p[:i]
		}
		seen[name] = true
	}
	for name := range seen {
		entries = append(entries, name)
	}
	sort.Strings(entries)
	return entries, true, nil
}

func Run(root string) []error {
	var errs []error
	add := func(s string, a ...any) { errs = append(errs, fmt.Errorf(s, a...)) }
	spec, extra, specErrs := loadWorkspaceSpec(root)
	errs = append(errs, specErrs...)
	if _, markerErr := os.Stat(filepath.Join(root, ".local", ".unburger-project")); markerErr == nil && spec == nil {
		add("workspace checks: %s is missing", workspaceSpecPath)
	}
	if spec != nil {
		b, e := os.ReadFile(filepath.Join(root, ".local", ".unburger-project"))
		if e != nil || strings.TrimSpace(string(b)) != "FFRS_UNBURGER_V1" {
			add("project identity missing")
		}
		for _, name := range spec.Authority {
			p := filepath.Join(root, ".local", "authority", name)
			st, e := os.Lstat(p)
			if e != nil || !st.Mode().IsRegular() {
				add("missing active rule: %s", name)
			}
		}
		baselineDocs, err := filepath.Glob(filepath.Join(root, ".local", "reference", "baseline", "docs", "*.md"))
		if err != nil || len(baselineDocs) != spec.BaselineDocs {
			add("incomplete reference rulebook set: expected %d docs, got %d", spec.BaselineDocs, len(baselineDocs))
		}
		for _, name := range spec.BaselineRequired {
			if _, err := os.Stat(filepath.Join(root, ".local", "reference", "baseline", name)); err != nil {
				add("missing inherited reference: %s", name)
			}
		}
		for _, name := range spec.Ledgers {
			st, e := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
			if e != nil || !st.Mode().IsRegular() {
				add("missing private corpus ledger: %s", name)
			}
		}
		for _, name := range spec.Directories {
			p := filepath.Join(root, ".local", filepath.FromSlash(name))
			st, e := os.Lstat(p)
			if e != nil || !st.IsDir() {
				add("missing private directory: %s", name)
			}
		}
	}
	b, e := os.ReadFile(filepath.Join(root, ".gitignore"))
	if e != nil || !strings.Contains(string(b), "/.local/") {
		add("private data must be gitignored")
	}
	rootAllowed := map[string]bool{"README.md": true, "go.mod": true, ".gitignore": true, "cmd": true, "internal": true, "tools": true, "schemas": true, "config": true, "translations": true, "glossary": true, "START.sh": true, "go.sum": true, "version.txt": true, ".local": true, ".git": true, "dist": true}
	gitEntries, inWorkTree, gitErr := gitRootEntries(root)
	if gitErr != nil {
		add("%v", gitErr)
	}
	if inWorkTree {
		for _, name := range gitEntries {
			if !rootAllowed[name] {
				add("unexpected public root entry: %s", name)
			}
		}
	} else {
		ents, e := os.ReadDir(root)
		if e != nil {
			return append(errs, e)
		}
		for _, v := range ents {
			if !rootAllowed[v.Name()] {
				add("unexpected public root entry: %s", v.Name())
			}
		}
	}
	for _, base := range []string{"cmd", "internal", "tools", "schemas", "config", "translations"} {
		walkErr := filepath.WalkDir(filepath.Join(root, base), func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				add("symlink in public tree: %s", p)
				return nil
			}
			if d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if strings.HasSuffix(rel, ".md") {
				add("internal documentation in public tree: %s", rel)
			}
			if strings.HasSuffix(rel, ".go") {
				if _, e := parser.ParseFile(token.NewFileSet(), p, nil, parser.ParseComments); e != nil {
					add("invalid Go: %s", rel)
				}
			}
			if strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, ".json") {
				b, e := os.ReadFile(p)
				if e != nil {
					add("read: %v", e)
				} else if identifierExposed(rel, b, extra) {
					add("private/internal identifier exposed: %s", rel)
				}
			}
			return nil
		})
		if walkErr != nil {
			add("scan %s: %v", base, walkErr)
		}
	}
	return errs
}
