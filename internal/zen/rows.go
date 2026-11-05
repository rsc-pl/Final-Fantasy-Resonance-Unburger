package zen

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// Row is one row of a cooked UDataTable whose boundaries are proven: the RowName and the row's
// unversioned header are consistent, and consecutive rows tile the table data exactly.
type Row struct {
	Index    int
	Name     string // RowName rendered as FName: name or name_number
	Start    int
	End      int
	Key      string // FText string-table key, empty when the row carries none
	Label    string // speaker label FName, empty when absent
	KeyOff   int
	LabelOff int
}

// labelWindow is the byte range, relative to the row start, where the speaker label FName is
// accepted. It is fixed by measurement on the confirmed scene, not per table.
const labelWindowLo, labelWindowHi = 40, 64

// ParseTableRows walks a DataTable's data area. A row start is an FName that resolves to the
// table's row-name template followed by a valid unversioned header. The walk is accepted only when
// the rows cover the data exactly: a gap or an overrun is an error, never a silently dropped row.
func ParseTableRows(pkg *Package, payload []byte, dataOffset int) ([]Row, error) {
	le := binary.LittleEndian
	type rowCandTmp struct {
		off, idx, num, values, zero int
	}
	var cands []rowCandTmp
	for p := dataOffset; p+12 <= len(payload); p++ {
		idx := int(le.Uint32(payload[p:]))
		if idx >= len(pkg.Names) {
			continue
		}
		// A row name is a table-local ASCII identifier. Asset paths, dotted paths and dialogue labels
		// are values inside a row, never a row name, so they cannot start one.
		if n := pkg.Names[idx]; !isRowNameIdent(n) {
			continue
		}
		num := int(le.Uint32(payload[p+4:]))
		if num > 4096 {
			continue
		}
		h, err := ParseUnversionedHeader(payload[p+8:])
		if err != nil || h.Values == 0 || h.Values > 64 {
			continue
		}
		cands = append(cands, rowCandTmp{p, idx, num, h.Values, h.ZeroBits})
	}
	// Two row layouts occur. In the first the table has one row-name template and the rows are its
	// numbered instances 0,1,2,...; in the second every row has its own name and the number is 0.
	// Both are only accepted when the rows tile the data exactly, and the second additionally
	// requires that most rows carry an FText key, which a nested header inside a row does not.
	var starts []int
	tryChain := func(mode int) []int {
		var best []int
		for _, c := range cands {
			if c.off < dataOffset || c.off > dataOffset+32 {
				continue
			}
			seq := []rowCandTmp{c}
			for _, d := range cands {
				prev := seq[len(seq)-1]
				if d.off <= prev.off {
					continue
				}
				if mode == 0 {
					if d.idx != c.idx || d.num != seq[0].num+len(seq) {
						continue
					}
				} else {
					if d.num != 0 || d.idx == prev.idx {
						continue
					}
				}
				seq = append(seq, d)
			}
			if len(seq) < 2 {
				continue
			}
			offs := make([]int, len(seq))
			for i, e := range seq {
				offs[i] = e.off
			}
			if offs[len(offs)-1] >= len(payload)-1 || len(offs) <= len(best) {
				continue
			}
			best = offs
		}
		return best
	}
	starts = tryChain(0)
	if starts == nil {
		starts = tryChain(1)
		if starts != nil {
			keys := 0
			for i, s := range starts {
				end := len(payload)
				if i+1 < len(starts) {
					end = starts[i+1]
				}
				seg := payload[s:end]
				for p := 0; p+9 < len(seg); p++ {
					if int8(seg[p+4]) != TextHistoryStringTable {
						continue
					}
					pos := p
					if t, err := ReadFText(seg, &pos); err == nil && t.Key != "" {
						keys++
						break
					}
				}
			}
			if keys*2 < len(starts) {
				starts = nil
			}
		}
	}
	if len(starts) == 0 {
		return nil, fmt.Errorf("table: no consistent row sequence")
	}

	// A row carries at least its FName (8 bytes) and the unversioned header (2 bytes). Anything
	// shorter is a false start inside another row's data, and a table that produces one is refused
	// rather than reported with a 4-byte "row".
	for i, s := range starts {
		end := len(payload)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		if end-s < 12 {
			return nil, fmt.Errorf("table: row %d is %d bytes, shorter than a name and a header", i, end-s)
		}
	}
	rows := make([]Row, 0, len(starts))
	for i, s := range starts {
		end := len(payload)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		num := le.Uint32(payload[s+4:])
		idx := int(le.Uint32(payload[s:]))
		name := fmt.Sprintf("<%d>", idx)
		if idx < len(pkg.Names) {
			name = pkg.Names[idx]
		}
		if num != 0 {
			name = fmt.Sprintf("%s_%d", name, num)
		}
		r := Row{Index: i, Name: name, Start: s, End: end, KeyOff: -1, LabelOff: -1}
		seg := payload[s:end]
		for p := 0; p+9 < len(seg); p++ {
			if int8(seg[p+4]) != TextHistoryStringTable {
				continue
			}
			pos := p
			if t, err := ReadFText(seg, &pos); err == nil && t.Key != "" {
				r.Key, r.KeyOff = t.Key, p
				break
			}
		}
		// The speaker label sits immediately before the FText value. The gap is 8 bytes in most
		// rows and 9 in some, so those two positions are tried, most specific first. A candidate
		// must be a resolvable FName with number 0 and must not be a path or an asset name.
		if r.KeyOff > 0 {
			for _, d := range []int{-8, -9} {
				p := r.KeyOff + d
				if p < 0 || p+8 > len(seg) {
					continue
				}
				idx := int(le.Uint32(seg[p:]))
				if le.Uint32(seg[p+4:]) != 0 || idx >= len(pkg.Names) {
					continue
				}
				n := pkg.Names[idx]
				if strings.ContainsAny(n, "/.") || strings.HasPrefix(n, "NewRow") ||
					strings.HasPrefix(n, "ST_") || strings.HasPrefix(n, "DT_") {
					continue
				}
				r.Label, r.LabelOff = n, p
				break
			}
		}
		rows = append(rows, r)
	}
	if rows[len(rows)-1].End != len(payload) {
		return nil, fmt.Errorf("table: rows end at %d, payload is %d", rows[len(rows)-1].End, len(payload))
	}
	return rows, nil
}

// isRowNameIdent reports whether a name can be a cooked row name: ASCII letters, digits and
// underscore only. Labels ("ニコル"), asset paths ("/Game/...") and keys with dots all fail.
func isRowNameIdent(n string) bool {
	if n == "" || len(n) > 96 {
		return false
	}
	for i := 0; i < len(n); i++ {
		c := n[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
		default:
			return false
		}
	}
	return true
}
