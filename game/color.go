package game

import (
	"fmt"
	"strings"
)

// Color is a tile color. The order matches row 0 of the standard wall.
type Color uint8

const (
	Blue Color = iota
	Yellow
	Red
	Black
	White
)

const NumColors = 5

var (
	colorNames   = [NumColors]string{"blue", "yellow", "red", "black", "white"}
	colorLetters = [NumColors]byte{'B', 'Y', 'R', 'K', 'W'}
)

func (c Color) String() string {
	if c < NumColors {
		return colorNames[c]
	}
	return fmt.Sprintf("Color(%d)", uint8(c))
}

// Letter returns the single-letter code used by the ASCII board (K = black).
func (c Color) Letter() byte { return colorLetters[c] }

// ParseColor accepts a color name or letter. "green" is an alias for black,
// since some editions print the fourth color green.
func ParseColor(s string) (Color, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "blue", "b":
		return Blue, nil
	case "yellow", "y":
		return Yellow, nil
	case "red", "r":
		return Red, nil
	case "black", "k", "green", "g":
		return Black, nil
	case "white", "w":
		return White, nil
	}
	return 0, fmt.Errorf("unknown color %q", s)
}

// WallCol returns the wall column where color c goes in row r.
func WallCol(r int, c Color) int { return (int(c) + r) % 5 }

// WallColor returns the color that belongs at wall position (r, col).
func WallColor(r, col int) Color { return Color((col - r + 5) % 5) }
