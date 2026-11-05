package corpuspack

import (
	"strings"

	"unburger/internal/zen"
)

// speakerFor derives the speaker context of one record from the proved key binding. The four levels stay
// apart: the label the game displays, the ST_CharaName entry it matched, the character identity and the
// runtime id. Only the first two are ever filled here, and the identity never follows from the entry.
func speakerFor(function string, b *zen.KeyBinding) Speaker {
	sp := Speaker{
		EvidenceRefs:       []string{},
		Identity:           nil,
		RuntimeCharacterID: nil,
	}
	switch function {
	case "dialogue_line":
		sp.Applicability = ApplicabilityRequired
	case "unclassified", "game_text_unclassified":
		// The function is not established, so whether a speaker applies is not established either. Declaring
		// NOT_APPLICABLE here would be a claim nobody proved.
		sp.Applicability = ApplicabilityUndetermined
		sp.LabelStatus = LabelUnknown
		return sp
	default:
		sp.Applicability = ApplicabilityNotApplicable
		sp.LabelStatus = LabelNotApplicable
		return sp
	}
	if b == nil {
		sp.LabelStatus = LabelUnknown
		return sp
	}
	switch b.Status {
	case zen.BindingConflict:
		sp.LabelStatus = LabelConflict
		// Two labels were read for the same key. Both are kept, neither is chosen.
		for _, lab := range strings.Split(b.Label, " | ") {
			lab = strings.TrimSpace(lab)
			if lab == "" {
				continue
			}
			cand := SpeakerCandidate{Label: lab, Evidence: []string{"key-identities:" + b.Namespace + "/" + b.Key}}
			for _, ev := range b.Evidence {
				if ev.Table != "" {
					cand.Table = ev.Table
					cand.Evidence = append(cand.Evidence, "table:"+ev.Table+" row:"+ev.Row)
					break
				}
			}
			sp.Candidates = append(sp.Candidates, cand)
		}
		if len(sp.Candidates) == 0 && b.Label != "" {
			lab := b.Label
			sp.LabelJP = &lab
			sp.LabelStatus = LabelConflict
		}
		return sp
	case zen.BindingMarker:
		// 空白 is an empty label and テロップ is on-screen text: both are recorded, neither identifies a person.
		sp.LabelStatus = LabelUnknown
		if b.Label != "" {
			lab := b.Label
			sp.LabelJP = &lab
		}
		return sp
	}
	if b.Label != "" {
		lab := b.Label
		sp.LabelJP = &lab
		sp.LabelStatus = LabelVerified
	}
	if b.Status == zen.BindingIdentified && b.NameEntry != "" {
		ne := b.NameEntry
		sp.NameEntry = &ne
	}
	if sp.LabelStatus == "" {
		sp.LabelStatus = LabelUnknown
	}
	return sp
}
