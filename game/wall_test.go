package game

import "testing"

// wallOf builds a wall from 5 row strings where 'x' marks a filled cell.
func wallOf(rows ...string) Wall {
	var w Wall
	for r, row := range rows {
		for col := 0; col < len(row); col++ {
			if row[col] == 'x' {
				w.set(r, col)
			}
		}
	}
	return w
}

func TestPlaceScoring(t *testing.T) {
	tests := []struct {
		name   string
		wall   []string
		r, col int
		want   int
	}{
		{"isolated", []string{".....", ".....", ".....", ".....", "....."}, 2, 2, 1},
		{"horizontal pair", []string{".....", ".....", "..x..", ".....", "....."}, 2, 3, 2},
		{"extends horizontal run", []string{".....", ".....", "xx...", ".....", "....."}, 2, 2, 3},
		{"vertical pair", []string{".....", "..x..", ".....", ".....", "....."}, 2, 2, 2},
		{"vertical run below", []string{".....", ".....", ".....", "..x..", "..x.."}, 2, 2, 3},
		{"corner L", []string{".....", "..x..", ".x...", ".....", "....."}, 2, 2, 4},
		{"T shape", []string{".....", ".....", ".x.x.", "..x..", "....."}, 2, 2, 5},
		{"cross", []string{".....", "..x..", ".x.x.", "..x..", "....."}, 2, 2, 6},
		{"joins two horizontal runs", []string{"xx.xx", ".....", ".....", ".....", "....."}, 0, 2, 5},
		{"joins two vertical runs", []string{"x....", "x....", ".....", "x....", "x...."}, 2, 0, 5},
		{"gap breaks the run", []string{"x...x", ".....", ".....", ".....", "....."}, 0, 2, 1},
		{"diagonal is not adjacent", []string{".x...", ".....", ".....", ".....", "....."}, 1, 2, 1},
		{"no wrap across rows", []string{"....x", ".....", ".....", ".....", "....."}, 1, 0, 1},
		{"no wrap across columns", []string{".....", ".....", ".....", ".....", "x...."}, 0, 1, 1},
		{"completes row and column", []string{"....x", "....x", "....x", "....x", "xxxx."}, 4, 4, 10},
		{"corner cell of full wall", []string{".xxxx", "xxxxx", "xxxxx", "xxxxx", "xxxxx"}, 0, 0, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := wallOf(tt.wall...)
			if got := w.Place(tt.r, tt.col); got != tt.want {
				t.Errorf("Place(%d,%d) = %d, want %d", tt.r, tt.col, got, tt.want)
			}
			if !w.Has(tt.r, tt.col) {
				t.Errorf("tile not set")
			}
		})
	}
}

func TestWallMirrorConsistent(t *testing.T) {
	w := wallOf("x.x..", ".x..x", "..xx.", "x...x", ".xx..")
	for r := 0; r < 5; r++ {
		for col := 0; col < 5; col++ {
			if w.Rows>>(r*5+col)&1 != w.Cols>>(col*5+r)&1 {
				t.Fatalf("mirror mismatch at (%d,%d)", r, col)
			}
		}
	}
}

func TestBonus(t *testing.T) {
	// Blue sits on the main diagonal: (0,0), (1,1), ...
	diag := wallOf("x....", ".x...", "..x..", "...x.", "....x")
	tests := []struct {
		name                   string
		w                      Wall
		rows, cols, colors, pt int
	}{
		{"empty", Wall{}, 0, 0, 0, 0},
		{"one row", wallOf(".....", "xxxxx"), 1, 0, 0, 2},
		{"one column", wallOf("..x..", "..x..", "..x..", "..x..", "..x.."), 0, 1, 0, 7},
		{"one color", diag, 0, 0, 1, 10},
		{"row almost full", wallOf("xxxx."), 0, 0, 0, 0},
		{"full wall", wallOf("xxxxx", "xxxxx", "xxxxx", "xxxxx", "xxxxx"), 5, 5, 5, 95},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.w.CompleteRows(); got != tt.rows {
				t.Errorf("rows = %d, want %d", got, tt.rows)
			}
			if got := tt.w.CompleteCols(); got != tt.cols {
				t.Errorf("cols = %d, want %d", got, tt.cols)
			}
			if got := tt.w.CompleteColors(); got != tt.colors {
				t.Errorf("colors = %d, want %d", got, tt.colors)
			}
			if got := tt.w.Bonus(); got != tt.pt {
				t.Errorf("bonus = %d, want %d", got, tt.pt)
			}
		})
	}
	if diag.ColorCount(Blue) != 5 || diag.ColorCount(Red) != 0 {
		t.Errorf("ColorCount wrong")
	}
}

func TestFloorPenalty(t *testing.T) {
	want := []int{0, -1, -2, -4, -6, -8, -11, -14}
	for n, w := range want {
		if got := FloorPenalty(uint8(n)); got != w {
			t.Errorf("FloorPenalty(%d) = %d, want %d", n, got, w)
		}
	}
}

func TestWallPattern(t *testing.T) {
	// Row 1 of the standard wall: white, blue, yellow, red, black.
	want := []Color{White, Blue, Yellow, Red, Black}
	for col, c := range want {
		if got := WallColor(1, col); got != c {
			t.Errorf("WallColor(1,%d) = %v, want %v", col, got, c)
		}
		if got := WallCol(1, c); got != col {
			t.Errorf("WallCol(1,%v) = %d, want %d", c, got, col)
		}
	}
}
