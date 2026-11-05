package build

// The Switch payload is a patch archive, not a loose file. The game's own localisation lives inside
// FFRS-Switch.pak; a loose file under romfs is not what the engine reads. This builds a small version 11 pak that
// carries only the modified archive, at the exact virtual path the shipped index names, and verifies it by
// reading the bytes back through the independent reader.

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"unburger/internal/iostore"
	"unburger/internal/pak"
	"unburger/internal/version"
)

// AllowEmptyContainer enables a package-free IoStore container. Its header write, readback and parse are
// structurally verified, but whether a shipped loader accepts a container with no packages is UNVERIFIED. It
// stays enabled so structurally valid test builds remain possible; artefact metadata keeps runtime UNVERIFIED
// until real runtime evidence exists.
var AllowEmptyContainer = true

// SwitchMountPoint is the mount point of the shipped archives, read from their own primary index.
const SwitchMountPoint = pak.DefaultMountPoint

// LocresVirtualPath is the mount-relative path of the game's English resource, taken from the shipped index.
const LocresVirtualPath = "FFRS/Content/Localization/Game/en/Game.locres"

// PatchPakName is the file name the console sees. The _P suffix marks a patch archive; the name sorts after the
// shipped FFRS-Switch.pak, which is what gives it priority.
// A leading z_ puts the archive last in the engine's alphabetical mount order, which is the convention every
// working patch archive for this title follows.
func PatchPakName(ver string) string {
	if ver == "" {
		return "z_ffrs_unburger_P.pak"
	}
	return "z_ffrs_unburger_v" + ver + "_P.pak"
}

// PakBuild is the result of composing a patch archive.
type PakBuild struct {
	Container   string
	Ucas        string
	TocBytes    int64
	UcasBytes   int64
	ContainerID string
	ChunkSHA1   []string
	Path        string
	Bytes       int64
	Entries     int
	Mount       string
	Virtual     string
	Primary     string
	PathHash    string
	FullDir     string
	Payload     string
}

// BuildPatchPak turns a directory of composed .locres files into one patch archive. Only the files that changed
// are passed in, so the archive stays small; every other entry of the game's own archive is untouched.
func BuildPatchPak(payloadDir, outPath, ver string) (PakBuild, error) {
	var res PakBuild
	names, err := filepath.Glob(filepath.Join(payloadDir, "*.locres"))
	if err != nil {
		return res, err
	}
	sort.Strings(names)
	if len(names) == 0 {
		return res, fmt.Errorf("no composed resource in %s", payloadDir)
	}
	var entries []pak.PatchEntry
	for _, n := range names {
		data, err := os.ReadFile(n)
		if err != nil {
			return res, err
		}
		// The composed payload keeps one file per shipped archive; the patch carries the game's own virtual path.
		entries = append(entries, pak.PatchEntry{Path: LocresVirtualPath, Data: data})
	}
	if len(entries) > 1 {
		return res, fmt.Errorf("a single patch archive cannot hold %d resources on one virtual path", len(entries))
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return res, err
	}
	// The container carries exactly one ContainerHeader chunk describing zero packages. The header round-trip is
	// checked below; runtime acceptance of a package-free container remains UNVERIFIED and is not inferred from
	// that structural readback.
	if !AllowEmptyContainer {
		return res, fmt.Errorf("Switch target blocked: no verified IoStore container for an asset-free mod")
	}
	base := strings.TrimSuffix(outPath, filepath.Ext(outPath))
	pakName := filepath.Base(outPath)
	idBytes := iostore.ContainerIDFor(pakName)
	var cid uint64
	for i := 0; i < 8; i++ {
		cid |= uint64(idBytes[7-i]) << (8 * uint(i))
	}
	hdr := iostore.NewEmptyContainerHeader(cid)
	payload := hdr.Serialize()
	if len(payload) != 64 {
		return res, fmt.Errorf("empty container header is %d bytes, want 64", len(payload))
	}
	chunk := iostore.ContainerChunk{ID: iostore.NewChunkID(cid, iostore.ChunkContainerHeader), Data: payload}
	ctr, err := iostore.WriteContainer(iostore.WriteOptions{Path: base + ".utoc", ID: idBytes, Chunks: []iostore.ContainerChunk{chunk}})
	if err != nil {
		return res, err
	}
	back, err := iostore.ChunkData(ctr.TocPath, 0)
	if err != nil {
		return res, err
	}
	if !bytes.Equal(back, payload) {
		return res, fmt.Errorf("container header chunk does not read back identical")
	}
	if got, err := iostore.ParseContainerHeader(back); err != nil || got.ContainerID != cid || len(got.PackageIDs) != 0 {
		return res, fmt.Errorf("container header does not parse back: %v", err)
	}
	written, err := pak.WritePatch(outPath, pak.PatchOptions{
		MountPoint: SwitchMountPoint, Entries: entries,
		UseZlib: true, BlockSize: pak.DefaultBlockSize, CompressionName: "Zlib",
	})
	if err != nil {
		return res, err
	}
	// Verify with the reader, not with the writer's own bookkeeping.
	idx, err := pak.ReadV11(outPath)
	if err != nil {
		return res, fmt.Errorf("written archive does not parse: %w", err)
	}
	if len(idx.Problems) > 0 {
		return res, fmt.Errorf("written archive reports %d problems, first: %s", len(idx.Problems), idx.Problems[0])
	}
	if idx.MountPoint != SwitchMountPoint {
		return res, fmt.Errorf("mount point %q, want %q", idx.MountPoint, SwitchMountPoint)
	}
	if idx.NumEntries != 1 || len(idx.Files) != 1 {
		return res, fmt.Errorf("archive holds %d entries, want 1", idx.NumEntries)
	}
	if idx.Files[0].Path != LocresVirtualPath {
		return res, fmt.Errorf("virtual path %q, want %q", idx.Files[0].Path, LocresVirtualPath)
	}
	locresBytes, err := pak.ReadV11Decoded(outPath, LocresVirtualPath, func(comp []byte, raw int) ([]byte, error) {
		r, zerr := zlib.NewReader(bytes.NewReader(comp))
		if zerr != nil {
			return nil, zerr
		}
		defer r.Close()
		b, zerr := io.ReadAll(r)
		if zerr != nil {
			return nil, zerr
		}
		if len(b) != raw {
			return nil, fmt.Errorf("block decoded to %d bytes, want %d", len(b), raw)
		}
		return b, nil
	})
	if err != nil {
		return res, err
	}
	if !bytes.Equal(locresBytes, entries[0].Data) {
		return res, fmt.Errorf("roundtrip returned %d bytes, wrote %d", len(locresBytes), len(entries[0].Data))
	}
	if version.Unresolved(string(locresBytes)) {
		return res, fmt.Errorf("the packaged resource still carries a version marker")
	}
	if len(written.Entries) != 1 || idx.Files[0].SHA1 == "" {
		return res, fmt.Errorf("payload sha1 was not available from both writer and reader")
	}
	if written.Entries[0].SHA1 != idx.Files[0].SHA1 {
		return res, fmt.Errorf("reader payload sha1 %s does not match writer %s", idx.Files[0].SHA1, written.Entries[0].SHA1)
	}
	fi, err := os.Stat(outPath)
	if err != nil {
		return res, err
	}
	res = PakBuild{
		Path: outPath, Bytes: fi.Size(), Entries: len(idx.Files), Mount: idx.MountPoint,
		Virtual: idx.Files[0].Path, Primary: idx.PrimarySHA1, PathHash: idx.PathHashSHA1,
		FullDir: idx.FullDirSHA1, Payload: idx.Files[0].SHA1,
	}
	// The archive and its container are structurally complete and read back through the independent reader.
	// Runtime acceptance of the package-free container remains UNVERIFIED and is reported as such, never inferred
	// from the structural readback.
	return res, nil
}

// SwitchSetComplete reports whether a payload directory holds the full triad a Switch mod needs. The requirement
// comes from the reference mod, which ships .pak, .utoc and .ucas under one basename, not from the extension names.
func SwitchSetComplete(dir, pakName string) error {
	base := strings.TrimSuffix(pakName, filepath.Ext(pakName))
	for _, ext := range []string{".pak", ".utoc", ".ucas"} {
		p := filepath.Join(dir, base+ext)
		fi, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("incomplete Switch set: %s is missing", filepath.Base(p))
		}
		if fi.Size() == 0 {
			return fmt.Errorf("incomplete Switch set: %s is empty", filepath.Base(p))
		}
	}
	return nil
}

// PatchPakRelPath is where the archive sits inside romfs so the engine finds it next to the shipped pak.
func PatchPakRelPath(name string) string {
	return strings.TrimPrefix("FFRS/Content/Paks/"+name, "/")
}
