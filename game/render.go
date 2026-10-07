package game

import (
	"fmt"
	"strings"
)

// String renders the state as an ASCII board. Pattern lines are
// right-aligned like the physical board; wall cells show an uppercase letter
// when filled and a lowercase one when empty.
func (s State) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Round %d  ", s.Round)
	if s.GameOver {
		b.WriteString("game over")
	} else {
		fmt.Fprintf(&b, "P%d to move", s.ToMove+1)
	}
	if s.TokenInCenter {
		b.WriteString("  token: center")
	} else {
		fmt.Fprintf(&b, "  token: P%d", s.NextFirst+1)
	}

	b.WriteString("\n\nFactories ")
	for f := range s.Factories {
		fmt.Fprintf(&b, " %d:[%s]", f+1, tileLetters(&s.Factories[f]))
	}
	fmt.Fprintf(&b, "\nCenter      [%s]", tileLetters(&s.Center))
	if s.TokenInCenter {
		b.WriteString(" +1st")
	}
	fmt.Fprintf(&b, "\nBag %s (%d)   Lid %s (%d)\n",
		countLetters(&s.Bag), sumColors(&s.Bag), countLetters(&s.Lid), sumColors(&s.Lid))

	for p := range s.Players {
		pb := &s.Players[p]
		mark := " "
		if !s.GameOver && int(s.ToMove) == p {
			mark = "*"
		}
		fmt.Fprintf(&b, "\nP%d%s score %d  floor %d (%d)\n", p+1, mark, pb.Score, pb.Floor, FloorPenalty(pb.Floor))
		for r := 0; r < NumRows; r++ {
			l := pb.Lines[r]
			b.WriteString("   ")
			b.WriteString(strings.Repeat("  ", 4-r))
			for j := 0; j <= r; j++ {
				if j < r+1-int(l.Count) {
					b.WriteString(". ")
				} else {
					fmt.Fprintf(&b, "%c ", l.Color.Letter())
				}
			}
			b.WriteString("|")
			for col := 0; col < 5; col++ {
				ch := WallColor(r, col).Letter()
				if !pb.Wall.Has(r, col) {
					ch += 'a' - 'A'
				}
				fmt.Fprintf(&b, " %c", ch)
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func tileLetters(counts *[NumColors]uint8) string {
	var parts []string
	for c, n := range counts {
		for range n {
			parts = append(parts, string(Color(c).Letter()))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func countLetters(counts *[NumColors]uint8) string {
	parts := make([]string, NumColors)
	for c, n := range counts {
		parts[c] = fmt.Sprintf("%c%d", Color(c).Letter(), n)
	}
	return strings.Join(parts, " ")
}
