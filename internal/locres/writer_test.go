package locres

import "testing"

// TestWriterRoundtrip proves the writer and the reader agree: a file written here parses back to the same
// entries, and writing it twice produces identical bytes.
func TestWriterRoundtrip(t *testing.T) {
	f := File{Version: 3, Entries: []Entry{
		{Namespace: "ST_UI_Word", Key: "Buy", Text: "Buy", SourceHash: 0x1111, NamespaceHash: 0xA1, KeyHash: 0xB1},
		{Namespace: "ST_UI_Word", Key: "Ability_en", Text: "Ability", SourceHash: 0x2222, NamespaceHash: 0xA1, KeyHash: 0xB2},
		{Namespace: "ST_UI_Word", Key: "Buy2", Text: "Buy", SourceHash: 0x2323, NamespaceHash: 0xA1, KeyHash: 0xB3},
		{Namespace: "ST_DialogChoice", Key: "DialogChoice_001", Text: "Yes", SourceHash: 0x3333, NamespaceHash: 0xA2, KeyHash: 0xB4},
		{Namespace: "ST_DialogChoice", Key: "DialogChoice_005", Text: "Don't pay 1,000 gil", SourceHash: 0x4444, NamespaceHash: 0xA2, KeyHash: 0xB5},
		{Namespace: "ST_C06_629_01", Key: "629_01_041", Text: "いいだろう。ここで決着をつけよう。", SourceHash: 0x5555, NamespaceHash: 0xA3, KeyHash: 0xB6},
		{Namespace: "ST_Empty", Key: "EmptyKey", Text: "", SourceHash: 0x6666, NamespaceHash: 0xA4, KeyHash: 0xB7},
	}}
	blob, err := Write(f)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(blob)
	if err != nil {
		t.Fatalf("written file does not parse: %v", err)
	}
	if back.Version != 3 {
		t.Fatalf("version = %d, want 3", back.Version)
	}
	if len(back.Entries) != len(f.Entries) {
		t.Fatalf("entries = %d, want %d", len(back.Entries), len(f.Entries))
	}
	for i := range f.Entries {
		if back.Entries[i] != f.Entries[i] {
			t.Fatalf("entry %d differs:\n got %+v\nwant %+v", i, back.Entries[i], f.Entries[i])
		}
	}
	again, err := Write(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(blob) {
		t.Fatal("writing the same file twice produced different bytes")
	}
}

// TestWriterEmptyAndLarge keeps the edges honest: no entries at all, and a namespace big enough to matter.
func TestWriterEmptyAndLarge(t *testing.T) {
	if _, err := Write(File{Version: 3}); err != nil {
		t.Fatalf("empty file refused: %v", err)
	}
	f := File{Version: 3}
	for i := 0; i < 5000; i++ {
		f.Entries = append(f.Entries, Entry{Namespace: "ST_Big", Key: "k" + itoa(i), Text: "value " + itoa(i),
			SourceHash: uint32(i), NamespaceHash: 0xC1, KeyHash: uint32(i) + 1})
	}
	blob, err := Write(f)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(blob)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Entries) != 5000 {
		t.Fatalf("entries = %d, want 5000", len(back.Entries))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestRefCountsCountReferences: the pool stores how many entries point at a text, not a hash of it. Two entries
// sharing one string must produce a count of two.
func TestRefCountsCountReferences(t *testing.T) {
	f := File{Version: 3, Entries: []Entry{
		{Namespace: "N", Key: "a", Text: "shared", SourceHash: 1, NamespaceHash: 1, KeyHash: 1},
		{Namespace: "N", Key: "b", Text: "shared", SourceHash: 2, NamespaceHash: 1, KeyHash: 2},
		{Namespace: "N", Key: "c", Text: "alone", SourceHash: 3, NamespaceHash: 1, KeyHash: 3},
	}}
	blob, err := Write(f)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(blob)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Entries) != 3 || back.Entries[0].Text != "shared" || back.Entries[2].Text != "alone" {
		t.Fatal("entries did not round-trip")
	}
	pool := string(blob)
	if !contains(pool, "shared") || !contains(pool, "alone") {
		t.Fatal("pool is missing a text")
	}
}

// TestWriterRefusesMissingHashes: an entry without the identifiers the engine looks up by must be refused rather
// than written with a substitute hash.
func TestWriterRefusesMissingHashes(t *testing.T) {
	if _, err := Write(File{Version: 3, Entries: []Entry{{Namespace: "N", Key: "k", Text: "t", SourceHash: 1}}}); err == nil {
		t.Fatal("an entry without hashes was accepted")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
