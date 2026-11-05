package diff

// pair.go aligns hunk lines into side-by-side rows: each row holds the
// old-side line (left) and the new-side line (right). Runs of removals
// and additions pair up by index and the shorter side is padded, so
// context and changed lines stay horizontally aligned (spec §7.3).

// SidePair is one rendered row of a side-by-side diff. Either side may be
// nil, meaning that side has no line on this row (a blank filler cell).
type SidePair struct {
	Left, Right *DiffLine
}

// PairLines pairs a hunk's lines old-against-new. A maximal run of
// changed lines pairs the i-th removal with the i-th addition; leftover
// removals keep an empty right side and leftover additions an empty
// left side. Context lines pair with themselves, preserving alignment
// across the hunk.
func PairLines(lines []DiffLine) []SidePair {
	pairs := make([]SidePair, 0, len(lines))
	i := 0
	for i < len(lines) {
		l := lines[i]
		switch l.Kind {
		case Context:
			both := l
			pairs = append(pairs, SidePair{Left: &both, Right: &both})
			i++
		case Del:
			// Collect the removal run, then the addition run that
			// follows it (git orders removals before additions).
			j := i
			var dels, adds []DiffLine
			for j < len(lines) && lines[j].Kind == Del {
				dels = append(dels, lines[j])
				j++
			}
			for j < len(lines) && lines[j].Kind == Add {
				adds = append(adds, lines[j])
				j++
			}
			n := max(len(dels), len(adds))
			for k := range n {
				var left, right *DiffLine
				if k < len(dels) {
					left = &dels[k]
				}
				if k < len(adds) {
					right = &adds[k]
				}
				pairs = append(pairs, SidePair{Left: left, Right: right})
			}
			i = j
		case Add:
			// An addition run with no preceding removals (possible in
			// hand-written or combined patches).
			j := i
			var adds []DiffLine
			for j < len(lines) && lines[j].Kind == Add {
				adds = append(adds, lines[j])
				j++
			}
			for k := range adds {
				pairs = append(pairs, SidePair{Right: &adds[k]})
			}
			i = j
		}
	}
	return pairs
}
