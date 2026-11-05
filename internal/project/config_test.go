package project

import (
	"errors"
	"testing"
)

func TestFailClosed(t *testing.T) {
	if !errors.Is((Config{}).ReadyForBuild(), ErrNotConfigured) {
		t.Fatal("unverified source allowed")
	}
}
