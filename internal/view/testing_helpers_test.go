package view

import (
	"strings"

	"github.com/gdamore/tcell/v2"
)

// readScreen returns the visible cell contents of a SimulationScreen as a
// newline-joined string suitable for substring assertions. Cells with no
// rune content are rendered as a single space so empty regions don't
// disturb the visual layout in test failure messages.
func readScreen(s tcell.SimulationScreen) string {
	cells, cw, h := s.GetContents()
	var b strings.Builder
	for row := 0; row < h; row++ {
		for col := 0; col < cw; col++ {
			idx := row*cw + col
			if idx >= len(cells) {
				break
			}
			if len(cells[idx].Runes) == 0 {
				b.WriteRune(' ')
				continue
			}
			for _, r := range cells[idx].Runes {
				if r == 0 {
					b.WriteRune(' ')
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteRune('\n')
	}
	return b.String()
}
