package main

// verify-pak reads an archive back through the version 11 reader: footer, index hashes, path hashes and the
// payload hash of every stored entry. It answers "is this file structurally intact", never "does the game load
// it" — the two are different claims.

import (
	"encoding/json"
	"fmt"
	"io"

	"unburger/internal/pak"
)

func runVerifyPak(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: unburger verify-pak <archive>")
		return 2
	}
	idx, err := pak.ReadV11(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "verify-pak FAILED:", err)
		return 1
	}
	out := struct {
		File            string   `json:"file"`
		MountPoint      string   `json:"mount_point"`
		Entries         int32    `json:"entries"`
		PathHashEntries int32    `json:"path_hash_entries"`
		PathHashMatched int      `json:"path_hash_matched"`
		DirectoryFiles  int      `json:"directory_files"`
		Compressed      int      `json:"compressed_entries"`
		PrimarySHA1     string   `json:"primary_index_sha1"`
		PathHashSHA1    string   `json:"path_hash_index_sha1"`
		FullDirSHA1     string   `json:"full_directory_index_sha1"`
		FooterIndexSHA1 string   `json:"footer_index_sha1"`
		State           string   `json:"state"`
		Problems        []string `json:"problems"`
	}{
		File: args[0], MountPoint: idx.MountPoint, Entries: idx.NumEntries, PathHashEntries: idx.PathHashCount,
		PathHashMatched: idx.PathHashMatch, DirectoryFiles: idx.FullDirFiles, Compressed: idx.Compressed,
		PrimarySHA1: idx.PrimarySHA1, PathHashSHA1: idx.PathHashSHA1, FullDirSHA1: idx.FullDirSHA1,
		FooterIndexSHA1: idx.FooterIndexSHA1, Problems: idx.Problems,
	}
	out.State = "PASS structural"
	if len(idx.Problems) > 0 || idx.FooterIndexSHA1 != idx.PrimarySHA1 {
		out.State = "FAIL"
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Fprintln(stdout, string(b))
	if out.State != "PASS structural" {
		return 1
	}
	return 0
}
