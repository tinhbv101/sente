// Package rules implements the Go (baduk) rules exactly as specified in
// docs/02-go-rules-spec.md. It is a pure package: no I/O, no clock, no logging,
// which is what lets the conformance vectors in rules-spec/ test it exhaustively.
package rules

// Color is the content of an intersection. offBoard is a sentinel filling the
// one-cell border, so neighbour loops need no bounds checks.
type Color uint8

const (
	Empty Color = iota
	Black
	White
	offBoard
)

func (c Color) Opponent() Color {
	switch c {
	case Black:
		return White
	case White:
		return Black
	default:
		return c
	}
}

func (c Color) String() string {
	switch c {
	case Black:
		return "black"
	case White:
		return "white"
	case Empty:
		return "empty"
	default:
		return "offboard"
	}
}

// Point is a board intersection. Row 0 is the top row, matching the wire format
// and the internal layout; display coordinates are converted separately.
type Point struct {
	Col int
	Row int
}

// SupportedSizes lists the board sizes the rules are specified for.
var SupportedSizes = [...]int{9, 13, 19}

func IsSupportedSize(size int) bool {
	for _, s := range SupportedSizes {
		if s == size {
			return true
		}
	}
	return false
}

// Board is an immutable position: every mutator returns a new Board.
type Board struct {
	size  int
	width int
	cells []Color
}

func NewBoard(size int) Board {
	if !IsSupportedSize(size) {
		panic("rules: unsupported board size")
	}
	width := size + 2
	cells := make([]Color, width*width)
	for i := range cells {
		cells[i] = offBoard
	}
	b := Board{size: size, width: width, cells: cells}
	for row := 0; row < size; row++ {
		for col := 0; col < size; col++ {
			cells[b.indexAt(col, row)] = Empty
		}
	}
	return b
}

func (b Board) Size() int { return b.size }

func (b Board) indexAt(col, row int) int { return (row+1)*b.width + (col + 1) }

func (b Board) index(p Point) int { return b.indexAt(p.Col, p.Row) }

func (b Board) pointAt(index int) Point {
	return Point{Col: index%b.width - 1, Row: index/b.width - 1}
}

// neighbours returns the four orthogonal indices. Diagonals never connect stones.
func (b Board) neighbours(index int) [4]int {
	return [4]int{index - b.width, index - 1, index + 1, index + b.width}
}

func (b Board) Contains(p Point) bool {
	return p.Col >= 0 && p.Row >= 0 && p.Col < b.size && p.Row < b.size
}

func (b Board) At(p Point) Color {
	if !b.Contains(p) {
		return offBoard
	}
	return b.cells[b.index(p)]
}

func (b Board) IsEmpty(p Point) bool { return b.Contains(p) && b.cells[b.index(p)] == Empty }

func (b Board) AllPoints() []Point {
	points := make([]Point, 0, b.size*b.size)
	for row := 0; row < b.size; row++ {
		for col := 0; col < b.size; col++ {
			points = append(points, Point{Col: col, Row: row})
		}
	}
	return points
}

func (b Board) Stones(c Color) []Point {
	var stones []Point
	for _, p := range b.AllPoints() {
		if b.cells[b.index(p)] == c {
			stones = append(stones, p)
		}
	}
	return stones
}

func (b Board) clone() Board {
	cells := make([]Color, len(b.cells))
	copy(cells, b.cells)
	return Board{size: b.size, width: b.width, cells: cells}
}

func (b Board) placing(p Point, c Color) Board {
	next := b.clone()
	next.cells[next.index(p)] = c
	return next
}

func (b Board) clearing(points []Point) Board {
	if len(points) == 0 {
		return b
	}
	next := b.clone()
	for _, p := range points {
		next.cells[next.index(p)] = Empty
	}
	return next
}

func (b Board) Equal(other Board) bool {
	if b.size != other.size {
		return false
	}
	for i := range b.cells {
		if b.cells[i] != other.cells[i] {
			return false
		}
	}
	return true
}

// chainAndLiberties flood-fills from start, collecting the connected group and
// counting its distinct liberties.
func (b Board) chainAndLiberties(start int) (stones []int, liberties int) {
	colour := b.cells[start]
	if colour != Black && colour != White {
		return nil, 0
	}
	visited := make([]bool, len(b.cells))
	libertySeen := make([]bool, len(b.cells))
	stack := []int{start}
	visited[start] = true

	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		stones = append(stones, current)
		for _, n := range b.neighbours(current) {
			switch b.cells[n] {
			case Empty:
				if !libertySeen[n] {
					libertySeen[n] = true
					liberties++
				}
			case colour:
				if !visited[n] {
					visited[n] = true
					stack = append(stack, n)
				}
			}
		}
	}
	return stones, liberties
}

// Chain returns the maximal connected group containing p.
func (b Board) Chain(p Point) []Point {
	if !b.Contains(p) {
		return nil
	}
	if c := b.cells[b.index(p)]; c != Black && c != White {
		return nil
	}
	indices, _ := b.chainAndLiberties(b.index(p))
	points := make([]Point, len(indices))
	for i, index := range indices {
		points[i] = b.pointAt(index)
	}
	return points
}

func (b Board) Liberties(p Point) int {
	if !b.Contains(p) {
		return 0
	}
	if c := b.cells[b.index(p)]; c != Black && c != White {
		return 0
	}
	_, liberties := b.chainAndLiberties(b.index(p))
	return liberties
}

// WireString is the compact form sent to clients: one character per intersection,
// left to right, top to bottom. See docs/06 §3.4.
func (b Board) WireString() string {
	out := make([]byte, 0, b.size*b.size)
	for row := 0; row < b.size; row++ {
		for col := 0; col < b.size; col++ {
			switch b.cells[b.indexAt(col, row)] {
			case Black:
				out = append(out, 'b')
			case White:
				out = append(out, 'w')
			default:
				out = append(out, '.')
			}
		}
	}
	return string(out)
}

func BoardFromWireString(size int, wire string) (Board, bool) {
	if !IsSupportedSize(size) || len(wire) != size*size {
		return Board{}, false
	}
	b := NewBoard(size)
	for offset := 0; offset < len(wire); offset++ {
		p := Point{Col: offset % size, Row: offset / size}
		switch wire[offset] {
		case 'b':
			b.cells[b.index(p)] = Black
		case 'w':
			b.cells[b.index(p)] = White
		case '.':
		default:
			return Board{}, false
		}
	}
	return b, true
}
