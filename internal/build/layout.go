package build

// Packaging turns one composed payload into one artefact for one target. A layout changes only the directory the
// payload is mounted from — never its content — and every archive carries a metadata file that names the
// edition, because a build without the edition in its name and metadata can be mistaken for another one.

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LocresRelPath is the authentic path of the English localisation resource inside romfs. Whether a loose file
// on this path is actually read by the game is a runtime question and is reported as unverified until tested.
const LocresRelPath = "FFRS/Content/Localization/Game/en/Game.locres"

// ModFolder is the directory an emulator loads a mod from: the emulator mounts the romfs under the mod's own
// folder, so this name is part of the package path, not decoration.
const ModFolder = "FFRS UNBURGER MOD"

// modRoot returns the top directory a layout mounts from. Atmosphère overlays the title's romfs through
// atmosphere/contents/<title id>/romfs; an emulator mounts a mod's own folder, whose romfs sits one level down.
func modRoot(t Target) string {
	if t.Platform == PlatformPC {
		// A PC build mounts pak files straight from its own content directory, so the archive starts at that
		// directory and carries no console overlay in front of it.
		return ""
	}
	if t.Layout == LayoutEmulator {
		return filepath.Join(ModFolder, "romfs")
	}
	return filepath.Join("atmosphere", "contents", TitleID, "romfs")
}

// mountRootFor reports the directory the archive mounts from; a PC build has no overlay root, so the reported
// value is the content directory the pak files sit in.
func mountRootFor(t Target, root string) string {
	if t.Platform == PlatformPC {
		return "FFRS/Content/Paks"
	}
	return root
}

// PackageLayout writes one zip for one target and returns its path.
func PackageLayout(payloadDir, distDir string, t Target, ver string) (string, error) {
	if !t.Ready && t.State != "" && t.State != StatePass {
		return "", fmt.Errorf("%s/%s/%s is %s: %s", t.Platform, t.Layout, t.Edition, t.State, t.Reason)
	}
	entries, err := os.ReadDir(payloadDir)
	if err != nil {
		return "", fmt.Errorf("nothing to package: %w", err)
	}
	var payload []string
	for _, e := range entries {
		if e.IsDir() || e.Name() == "build-report.json" {
			continue
		}
		payload = append(payload, e.Name())
	}
	if len(payload) == 0 {
		return "", fmt.Errorf("no composed resource in %s", payloadDir)
	}
	sort.Strings(payload)
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(distDir, ArtifactName(t, ver))
	// Build the archive under a temporary name and publish it with one rename: a failed write must not leave a
	// truncated file at the name a user would pick up.
	f, err := os.CreateTemp(distDir, "ffrs-tmp-*.zip")
	if err != nil {
		return "", err
	}
	tmpPath := f.Name()
	cleanup := func() {
		f.Close()
		os.Remove(tmpPath)
	}
	zw := zip.NewWriter(f)
	root := modRoot(t)
	// The engine reads the resource from a pak, not from a loose file, so the payload is packed into a patch
	// archive before it leaves the builder.
	pakName := PatchPakName(ver)
	pakPath := filepath.Join(payloadDir, pakName)
	packed, err := BuildPatchPak(payloadDir, pakPath, ver)
	if err != nil {
		return "", err
	}
	if err := SwitchSetComplete(payloadDir, pakName); err != nil {
		return "", err
	}
	if err := addFile(zw, filepath.Join(root, filepath.FromSlash(PatchPakRelPath(pakName))), pakPath); err != nil {
		return "", err
	}
	// The container pair travels with the archive under the same basename; a mod without it is incomplete.
	for _, ext := range []string{".utoc", ".ucas"} {
		src := strings.TrimSuffix(pakPath, filepath.Ext(pakPath)) + ext
		if err := addFile(zw, filepath.Join(root, filepath.FromSlash(PatchPakRelPath(strings.TrimSuffix(pakName, ".pak")+ext))), src); err != nil {
			return "", err
		}
	}
	meta := map[string]interface{}{
		"artifact":         ArtifactName(t, ver),
		"version":          ver,
		"platform":         t.Platform,
		"layout":           t.Layout,
		"edition":          t.Edition,
		"title_id":         TitleID,
		"format":           "layeredfs-pak-v11",
		"resource":         LocresVirtualPath,
		"pak":              pakName,
		"pak_sha1":         packed.Primary,
		"pak_payload_sha1": packed.Payload,
		"runtime":          RuntimeUnknown,
		"mount_root":       mountRootFor(t, root),
		"generated_by":     "unburger build all",
	}
	mb, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", err
	}
	if t.Platform == PlatformPC {
		// A PC package is unpacked straight into the game folder, so it carries only the three mod files; the
		// release metadata is kept beside the archive instead of inside it.
		sidecar := filepath.Join(distDir, strings.TrimSuffix(ArtifactName(t, ver), ".zip")+".mod-meta.json")
		if err := os.WriteFile(sidecar, append(mb, '\n'), 0o644); err != nil {
			return "", err
		}
	} else {
		w, err := zw.Create("mod-meta.json")
		if err != nil {
			return "", err
		}
		if _, err := w.Write(append(mb, '\n')); err != nil {
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		cleanup()
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	return target, nil
}

func addFile(zw *zip.Writer, rel, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	w, err := zw.Create(filepath.ToSlash(rel))
	if err != nil {
		return err
	}
	_, err = io.Copy(w, in)
	return err
}
