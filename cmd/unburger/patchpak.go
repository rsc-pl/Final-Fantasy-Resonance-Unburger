package main

// patch-pak rebuilds one archive with a single replaced resource. This is the route a working localisation mod
// for this title takes: the shipped archive is re-written rather than a second archive being added next to it.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"unburger/internal/pak"
)

func runPatchPak(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("patch-pak", flag.ContinueOnError)
	fs.SetOutput(stderr)
	src := fs.String("in", "", "source archive")
	dst := fs.String("out", "", "output archive (may equal --in, which then requires --backup)")
	locres := fs.String("locres", "", "composed resource to place at the game's virtual path")
	backup := fs.String("backup", "", "backup written before an in-place replacement")
	expect := fs.String("expect", "", "accepted sha256 of the source archive (repeatable through --expect-list)")
	expectList := fs.String("expect-list", "", "file with one accepted sha256 per line")
	virtual := fs.String("virtual", pak.VirtualPath, "mount-relative path of the resource")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *src == "" || *dst == "" || *locres == "" {
		fmt.Fprintln(stderr, "usage: unburger patch-pak --in <pak> --out <pak> --locres <file> [--backup <file>] [--expect <sha256>]")
		return 2
	}
	payload, err := os.ReadFile(*locres)
	if err != nil {
		fmt.Fprintln(stderr, "patch-pak:", err)
		return 1
	}
	var known []string
	if *expect != "" {
		known = append(known, *expect)
	}
	if *expectList != "" {
		b, err := os.ReadFile(*expectList)
		if err != nil {
			fmt.Fprintln(stderr, "patch-pak:", err)
			return 1
		}
		for _, line := range splitLines(string(b)) {
			if line != "" {
				known = append(known, line)
			}
		}
	}
	res, err := pak.PatchArchive(pak.InPlaceRequest{
		Src: *src, Dst: *dst, Backup: *backup, VirtualPath: *virtual, Payload: payload, KnownSHA256: known,
	})
	if err != nil {
		fmt.Fprintln(stderr, "patch-pak FAILED:", err)
		return 1
	}
	out := struct {
		Source      string `json:"source"`
		Output      string `json:"output"`
		SourceSHA   string `json:"source_sha256"`
		OutputSHA   string `json:"output_sha256"`
		SourceBytes int64  `json:"source_bytes"`
		OutputBytes int64  `json:"output_bytes"`
		Entries     int    `json:"entries"`
		Copied      int    `json:"copied_entries"`
		Target      string `json:"target"`
		TargetOld   int64  `json:"target_bytes_before"`
		TargetNew   int64  `json:"target_bytes_after"`
		TargetSHA1  string `json:"target_sha1"`
		PrimarySHA1 string `json:"primary_index_sha1"`
		PathHash1   string `json:"path_hash_index_sha1"`
		FullDir1    string `json:"full_directory_index_sha1"`
		State       string `json:"state"`
	}{*src, *dst, res.SrcSHA256, res.DstSHA256, res.SrcBytes, res.DstBytes, res.Entries, res.Copied,
		*virtual, res.TargetOld, res.TargetNew, res.TargetSHA1, res.PrimarySHA1, res.PathHashSHA1, res.FullDirSHA1,
		"PASS structural"}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Fprintln(stdout, string(b))
	return 0
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' || r == '\r' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
