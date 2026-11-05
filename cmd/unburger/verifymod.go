package main

// verify-mod answers one question: if this archive is unpacked where the user says it will be, at which path does
// the game look for the resource, and does the archive actually put it there? It reports the layout it recognises
// and refuses to call a layout valid on the strength of the file name alone.

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const switchTitleID = "0100606026F12000"

func inspectMod(target string) (string, error) {
	var names []string
	if st, err := os.Stat(target); err == nil && st.IsDir() {
		root := target
		err := filepath.Walk(root, func(p string, info os.FileInfo, e error) error {
			if e != nil || info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			names = append(names, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return "", err
		}
	} else {
		r, err := zip.OpenReader(target)
		if err != nil {
			return "", err
		}
		defer r.Close()
		for _, f := range r.File {
			if strings.HasSuffix(f.Name, "/") {
				continue
			}
			names = append(names, f.Name)
		}
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "archive: %s\nfiles: %d\n\n", target, len(names))
	for _, n := range names {
		fmt.Fprintf(&b, "  %s\n", n)
	}
	layout, romfsAt, resourceAt := classify(names)
	fmt.Fprintf(&b, "\nlayout: %s\n", layout)
	switch layout {
	case "atmosphere":
		fmt.Fprintf(&b, "extract into the SD card root; the game reads %s\n", romfsAt)
		fmt.Fprintf(&b, "install path: atmosphere/contents/%s/romfs/\n", switchTitleID)
	case "emulator":
		fmt.Fprintf(&b, "romfs root inside the archive: %s\n", romfsAt)
		fmt.Fprintf(&b, "install path: <emulator>/mods/contents/%s/<mod directory>/romfs/\n", switchTitleID)
		fmt.Fprintf(&b, "the archive must be placed so that its %s directory becomes the romfs of that mod directory\n", romfsAt)
	default:
		b.WriteString("no romfs root found: this archive would not mount at all\n")
	}
	if resourceAt != "" {
		fmt.Fprintf(&b, "resource: %s\n", resourceAt)
	} else {
		b.WriteString("resource: no patch archive inside the archive\n")
	}
	if !strings.Contains(strings.Join(names, "\n"), switchTitleID) && layout == "emulator" {
		b.WriteString("note: the title id is not part of the archive path, so the emulator directory must supply it\n")
	}
	return b.String(), nil
}

// classify finds the romfs root of an archive and the localisation resource beneath it.
func classify(names []string) (layout, romfsAt, resourceAt string) {
	for _, n := range names {
		if strings.HasPrefix(n, "atmosphere/contents/"+switchTitleID+"/romfs/") {
			layout = "atmosphere"
			romfsAt = "atmosphere/contents/" + switchTitleID + "/romfs/"
			break
		}
		if strings.Contains(n, "/romfs/") && strings.HasPrefix(n, "FF-") {
			layout = "emulator"
			romfsAt = n[:strings.Index(n, "/romfs/")+len("/romfs")+1]
			break
		}
	}
	for _, n := range names {
		if strings.HasSuffix(n, ".locres") {
			resourceAt = n
			break
		}
		if strings.HasSuffix(n, "_P.pak") {
			resourceAt = n + " (patch archive)"
			break
		}
	}
	return layout, romfsAt, resourceAt
}
