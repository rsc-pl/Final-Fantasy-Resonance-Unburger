package ingest

import "sort"

// SetDiff accounts for what an update did to one logical set.
type SetDiff struct {
	Set        string   `json:"set"`
	Platform   string   `json:"platform"`
	From       string   `json:"from"`
	To         string   `json:"to"`
	Added      int      `json:"added"`
	Removed    int      `json:"removed"`
	Changed    int      `json:"changed"`
	Unchanged  int      `json:"unchanged"`
	AddedIDs   []string `json:"added_sample,omitempty"`
	RemovedIDs []string `json:"removed_sample,omitempty"`
	ChangedIDs []string `json:"changed_sample,omitempty"`
}

const diffSample = 10

type textIndex struct {
	text  map[string]string // key -> text
	order []string
}

func indexOf(inv inventory) textIndex {
	idx := textIndex{text: make(map[string]string, len(inv.Entries))}
	for _, e := range inv.Entries {
		k := e.Namespace + "\x00" + e.Key
		idx.text[k] = e.Text
	}
	for k := range idx.text {
		idx.order = append(idx.order, k)
	}
	sort.Strings(idx.order)
	return idx
}

// diffLayers compares two layers of one set and returns the accounting plus the ids the
// caller should attach to each class. Text comparison is exact: no normalisation, because a
// whitespace-only change is still a source change that has to be reviewed.
func diffLayers(setID, platform string, from, to textIndex, fromLayer, toLayer string) SetDiff {
	d := SetDiff{Set: setID, Platform: platform, From: fromLayer, To: toLayer}
	for k, toText := range to.text {
		fromText, ok := from.text[k]
		switch {
		case !ok:
			d.Added++
			if len(d.AddedIDs) < diffSample {
				d.AddedIDs = append(d.AddedIDs, setID+":"+k)
			}
		case fromText != toText:
			d.Changed++
			if len(d.ChangedIDs) < diffSample {
				d.ChangedIDs = append(d.ChangedIDs, setID+":"+k)
			}
		default:
			d.Unchanged++
		}
	}
	for k := range from.text {
		if _, ok := to.text[k]; !ok {
			d.Removed++
			if len(d.RemovedIDs) < diffSample {
				d.RemovedIDs = append(d.RemovedIDs, setID+":"+k)
			}
		}
	}
	sort.Strings(d.AddedIDs)
	sort.Strings(d.RemovedIDs)
	sort.Strings(d.ChangedIDs)
	return d
}
