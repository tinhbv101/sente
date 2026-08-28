package rules

// Star-point placement for handicap stones (docs/02 §8).

// starLines returns the three star lines for a board size, low to high. Corners,
// edge midpoints and the centre are all combinations of these.
func starLines(size int) []int {
	switch size {
	case 19:
		return []int{3, 9, 15}
	case 13:
		return []int{3, 6, 9}
	case 9:
		return []int{2, 4, 6}
	default:
		return nil
	}
}

func StarPoints(size int) []Point {
	lines := starLines(size)
	points := make([]Point, 0, len(lines)*len(lines))
	for _, row := range lines {
		for _, col := range lines {
			points = append(points, Point{Col: col, Row: row})
		}
	}
	return points
}

// HandicapStones places stones in the conventional order: two corners first, then
// the remaining two, then edges, with the centre always taken last on odd counts.
func HandicapStones(count, size int) []Point {
	lines := starLines(size)
	if count < 2 || count > 9 || len(lines) != 3 {
		return nil
	}
	low, mid, high := lines[0], lines[1], lines[2]
	upperRight := Point{Col: high, Row: low}
	lowerLeft := Point{Col: low, Row: high}
	lowerRight := Point{Col: high, Row: high}
	upperLeft := Point{Col: low, Row: low}
	leftEdge := Point{Col: low, Row: mid}
	rightEdge := Point{Col: high, Row: mid}
	bottomEdge := Point{Col: mid, Row: high}
	topEdge := Point{Col: mid, Row: low}
	centre := Point{Col: mid, Row: mid}
	corners := []Point{upperRight, lowerLeft, lowerRight, upperLeft}

	switch count {
	case 2:
		return []Point{upperRight, lowerLeft}
	case 3:
		return []Point{upperRight, lowerLeft, lowerRight}
	case 4:
		return corners
	case 5:
		return append(corners, centre)
	case 6:
		return append(corners, leftEdge, rightEdge)
	case 7:
		return append(corners, leftEdge, rightEdge, centre)
	case 8:
		return append(corners, leftEdge, rightEdge, bottomEdge, topEdge)
	default:
		return append(corners, leftEdge, rightEdge, bottomEdge, topEdge, centre)
	}
}
