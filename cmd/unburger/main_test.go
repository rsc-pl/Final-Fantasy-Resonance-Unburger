package main

import (
	"strings"
	"testing"

	"unburger/internal/project"
)

func TestUnknownCommandFails(t *testing.T) {
	if run([]string{"invalid"}) == 0 {
		t.Fatal("invalid command succeeded")
	}
}

// TestReleaseStatusSeparatesRuntimeFromBuild: ReadyForBuild includes runtime_pass, so its failure is named as a
// release gate instead of being printed as "Build safe: false" while structural test builds remain permitted.
func TestReleaseStatusSeparatesRuntimeFromBuild(t *testing.T) {
	c := project.Config{Status: project.Status{SourceVerified: true, AdapterImplemented: true, SafeToPatch: true}}
	got := releaseStatus(c)
	if strings.Contains(got, "release ready") {
		t.Fatalf("unverified runtime reported as release ready: %s", got)
	}
	if !strings.Contains(got, "runtime=false") || !strings.Contains(got, "not release-ready") {
		t.Fatalf("release status hides the real gate: %s", got)
	}
	c.Status.RuntimePass = true
	c.Status.ReleaseApproved = true
	if got := releaseStatus(c); got != "release ready" {
		t.Fatalf("complete release status = %q", got)
	}
}
