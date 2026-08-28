package rules

import (
	"fmt"
	"strconv"
	"strings"
)

// ColumnLetters omits I so it cannot be misread as the digit 1.
const ColumnLetters = "ABCDEFGHJKLMNOPQRST"

// CoordinateText renders a point as the human coordinate used on the wire and in
// logs: column letter plus a row number counted from the bottom (docs/06 §1).
func CoordinateText(p Point, size int) string {
	return fmt.Sprintf("%c%d", ColumnLetters[p.Col], size-p.Row)
}

// ParseCoordinate reads "d4" or "Q16". ok is false for anything off the board.
func ParseCoordinate(text string, size int) (Point, bool) {
	trimmed := strings.ToUpper(strings.TrimSpace(text))
	if len(trimmed) < 2 {
		return Point{}, false
	}
	col := strings.IndexByte(ColumnLetters, trimmed[0])
	if col < 0 || col >= size {
		return Point{}, false
	}
	number, err := strconv.Atoi(trimmed[1:])
	if err != nil || number < 1 || number > size {
		return Point{}, false
	}
	return Point{Col: col, Row: size - number}, true
}

// SGF uses a-s on both axes with the origin at the top left -- a different system
// from the display coordinates above, and a classic source of off-by-ones.
func SGFText(p Point) string {
	return string([]byte{byte('a' + p.Col), byte('a' + p.Row)})
}

func ParseSGFPoint(text string, size int) (Point, bool) {
	if len(text) != 2 {
		return Point{}, false
	}
	col, row := int(text[0]-'a'), int(text[1]-'a')
	if col < 0 || row < 0 || col >= size || row >= size {
		return Point{}, false
	}
	return Point{Col: col, Row: row}, true
}
