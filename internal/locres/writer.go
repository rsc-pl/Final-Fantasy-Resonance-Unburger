package locres

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"unicode/utf16"
)

// Write serialises a locres file in format version 3. The layout mirrors Parse exactly, so a file written here
// reads back entry for entry; the caller is expected to check that with Parse before shipping anything.
func Write(f File) ([]byte, error) {
	if len(f.Entries) > maxItems {
		return nil, fmt.Errorf("too many entries: %d", len(f.Entries))
	}
	version := f.Version
	if version == 0 {
		version = 3
	}
	if version != 3 {
		return nil, fmt.Errorf("writer supports locres version 3, not %d", version)
	}

	// The text pool holds every distinct text once, in first-seen order, so the output is deterministic. Each
	// entry also records how often it occurs: the pool stores that reference count next to the text, and the
	// engine uses it to size its own tables rather than hashing the text.
	pool := []string{}
	index := map[string]int{}
	refs := []int{}
	for _, e := range f.Entries {
		i, ok := index[e.Text]
		if !ok {
			i = len(pool)
			index[e.Text] = i
			pool = append(pool, e.Text)
			refs = append(refs, 0)
		}
		refs[i]++
	}

	var entries bytes.Buffer
	writeCount(&entries, len(f.Entries))
	byNamespace := map[string][]Entry{}
	namespaceOrder := []string{}
	for _, e := range f.Entries {
		if _, ok := byNamespace[e.Namespace]; !ok {
			namespaceOrder = append(namespaceOrder, e.Namespace)
		}
		byNamespace[e.Namespace] = append(byNamespace[e.Namespace], e)
	}
	writeCount(&entries, len(namespaceOrder))
	for _, ns := range namespaceOrder {
		list := byNamespace[ns]
		if list[0].NamespaceHash == 0 {
			return nil, fmt.Errorf("namespace %q carries no hash from the source resource", ns)
		}
		if err := binary.Write(&entries, binary.LittleEndian, list[0].NamespaceHash); err != nil {
			return nil, err
		}
		if err := writeFString(&entries, ns); err != nil {
			return nil, err
		}
		writeCount(&entries, len(list))
		for _, e := range list {
			if e.KeyHash == 0 {
				return nil, fmt.Errorf("%s/%s carries no key hash from the source resource", ns, e.Key)
			}
			if err := binary.Write(&entries, binary.LittleEndian, e.KeyHash); err != nil {
				return nil, err
			}
			if err := writeFString(&entries, e.Key); err != nil {
				return nil, err
			}
			if err := binary.Write(&entries, binary.LittleEndian, e.SourceHash); err != nil {
				return nil, err
			}
			if err := binary.Write(&entries, binary.LittleEndian, int32(index[e.Text])); err != nil {
				return nil, err
			}
		}
	}

	// The pool is addressed by an absolute offset written straight after the version byte.
	header := make([]byte, 0, 16+1+8)
	header = append(header, magic[:]...)
	header = append(header, version)
	poolOffset := int64(len(header) + 8 + entries.Len())
	var off [8]byte
	binary.LittleEndian.PutUint64(off[:], uint64(poolOffset))
	header = append(header, off[:]...)

	var body bytes.Buffer
	body.Write(header)
	body.Write(entries.Bytes())
	writeCount(&body, len(pool))
	for i, s := range pool {
		if err := writeFString(&body, s); err != nil {
			return nil, err
		}
		if err := binary.Write(&body, binary.LittleEndian, int32(refs[i])); err != nil {
			return nil, err
		}
	}
	return body.Bytes(), nil
}

// writeCount writes a non-negative int32 count.
func writeCount(w *bytes.Buffer, n int) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(int32(n)))
	w.Write(b[:])
}

// writeString writes an FString: ANSI with a terminating NUL, or UTF-16 with a negative length when the text
// does not fit ANSI cleanly.
func writeFString(w *bytes.Buffer, s string) error {
	if sascii(s) {
		writeCount(w, len(s)+1)
		w.WriteString(s)
		w.WriteByte(0)
		return nil
	}
	u := utf16.Encode([]rune(s))
	writeCount(w, -(len(u) + 1))
	var b [2]byte
	for _, v := range u {
		binary.LittleEndian.PutUint16(b[:], v)
		w.Write(b[:])
	}
	w.Write([]byte{0, 0})
	return nil
}

func sascii(s string) bool {
	for _, r := range s {
		if r == 0 || r > 0x7f {
			return false
		}
	}
	return true
}

// hashString is the identifier the format stores beside each name. The reader ignores it, so a writer must keep
// it stable and deterministic rather than guess the engine's exact function.
func hashString(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// SortEntries orders entries by namespace and key, which is the order the engine writes them in and the order a
// diff of two builds should show.
func SortEntries(entries []Entry) []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Key < out[j].Key
	})
	return out
}
