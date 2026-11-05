package ingest

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// sceneSuffix matches the numeric tail of a dialogue key, e.g. 12Arms_00_001.
var sceneSuffix = regexp.MustCompile(`_?([0-9]+)$`)

// keyScene returns the locres namespace of a record id and the numeric tail of its key.
func keyScene(r Record) (string, int, bool) {
	ns := ""
	if r.ContextRef != "" {
		if i := strings.Index(r.ContextRef, "namespace="); i >= 0 {
			rest := r.ContextRef[i+len("namespace="):]
			if j := strings.Index(rest, " key="); j >= 0 {
				ns = rest[:j]
			}
		}
	}
	key := ""
	if i := strings.Index(r.ContextRef, " key="); i >= 0 {
		key = r.ContextRef[i+len(" key="):]
	}
	if m := sceneSuffix.FindStringSubmatch(key); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return ns, n, true
		}
	}
	return ns, 0, false
}

// AnnotateDialogue adds scene, order and neighbour links to dialogue records, and states
// explicitly that speaker and addressee are unproven. A locres archive carries text and a
// key only; attributing a speaker from wording or from the scene id would be a guess, so it
// is left UNKNOWN with confidence "none" until an asset-level source proves it.
func AnnotateDialogue(jp, loc []Record) {
	type slot struct {
		idx int
		seq int
		key string
	}
	byScene := map[string][]slot{}
	for i := range jp {
		ns, seq, ok := keyScene(jp[i])
		if !ok || !strings.HasPrefix(ns, "ST_") {
			continue
		}
		jp[i].Scene = ns
		jp[i].Seq = seq
		key := ""
		if k := strings.Index(jp[i].ContextRef, " key="); k >= 0 {
			key = jp[i].ContextRef[k+len(" key="):]
		}
		byScene[ns] = append(byScene[ns], slot{idx: i, seq: seq, key: key})
	}
	for _, slots := range byScene {
		sort.Slice(slots, func(a, b int) bool {
			if slots[a].seq != slots[b].seq {
				return slots[a].seq < slots[b].seq
			}
			return slots[a].key < slots[b].key
		})
		for n, s := range slots {
			r := &jp[s.idx]
			if n > 0 {
				r.PrevRecordID = jp[slots[n-1].idx].RecordID
			}
			if n+1 < len(slots) {
				r.NextRecordID = jp[slots[n+1].idx].RecordID
			}
			r.Speaker = "UNKNOWN"
			r.Addressee = "UNKNOWN"
			r.SpeakerConfidence = "none"
			r.SpeakerEvidence = "locres archive has no speaker, addressee or branch field"
		}
	}
	mirror := make(map[string]Record, len(jp))
	for _, r := range jp {
		if r.Scene != "" {
			mirror[r.RecordID] = r
		}
	}
	for i := range loc {
		if src, ok := mirror[loc[i].RecordID]; ok {
			loc[i].Scene = src.Scene
			loc[i].Seq = src.Seq
			loc[i].PrevRecordID = src.PrevRecordID
			loc[i].NextRecordID = src.NextRecordID
			loc[i].Speaker = src.Speaker
			loc[i].Addressee = src.Addressee
			loc[i].SpeakerConfidence = src.SpeakerConfidence
			loc[i].SpeakerEvidence = src.SpeakerEvidence
		}
	}
}
