package rules

// Board hashing. The constants live in zobrist_table.go, generated from
// rules-spec/zobrist_table.json so this engine and GoKit hash identically.

// zobristValue indexes the shared table by row*size+col; smaller boards use the
// leading slice of the 19x19 table.
func zobristValue(size, col, row int, c Color) uint64 {
	index := row*size + col
	if c == Black {
		return blackTable[index]
	}
	return whiteTable[index]
}

// ZobristHash is the XOR of every occupied intersection. Colour to move is
// deliberately excluded: positional superko compares boards only (docs/02 §4.2).
func ZobristHash(b Board) uint64 {
	var hash uint64
	for row := 0; row < b.size; row++ {
		for col := 0; col < b.size; col++ {
			switch c := b.cells[b.indexAt(col, row)]; c {
			case Black, White:
				hash ^= zobristValue(b.size, col, row, c)
			}
		}
	}
	return hash
}
