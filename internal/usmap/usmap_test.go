package usmap

import (
	"os"
	"testing"
)

// TestStrictStructWalkOverRealMap requires the mapping file that ships with the project to decode
// sequentially, record after record, with no resynchronisation — and to contain the structs the
// dialogue pipeline depends on.
func TestStrictStructWalkOverRealMap(t *testing.T) {
	path := "../../.local/toolchain/maps/FFRS-5.6.1-0+UE5-24b12662.usmap"
	if _, err := os.Stat(path); err != nil {
		t.Skip("mapping file not present on this host")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	// The strict walk is the honest measure of the reader: it stops at the first record it cannot
	// decode, so "13005 of 13005" means every struct record in the file decodes in order with no
	// resynchronisation.
	n, walkErr := m.StrictStructWalk(data)
	if walkErr != nil || n != 13005 {
		t.Fatalf("strict walk decoded %d of 13005 structs: %v", n, walkErr)
	}
	for name, want := range map[string]int{"TalkEventRowData": 10, "ChatRowData": 4, "TalkEventData": 70} {
		props, err := m.Flatten(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(props) != want {
			t.Fatalf("%s has %d properties, want %d", name, len(props), want)
		}
	}
}
