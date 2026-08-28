package rules

// PassAliveChains implements Benson's algorithm for unconditional life.
//
// A chain is pass-alive when the opponent cannot capture it even if the owner
// never answers. The result is exact, so these chains must never be suggested as
// dead during scoring (docs/02 §6.4).
func (b Board) PassAliveChains(player Color) [][]Point {
	opponent := player.Opponent()

	// 1. Every chain of the player.
	var chains [][]int
	chainOf := make([]int, len(b.cells))
	for i := range chainOf {
		chainOf[i] = -1
	}
	for index := range b.cells {
		if b.cells[index] != player || chainOf[index] != -1 {
			continue
		}
		stones, _ := b.chainAndLiberties(index)
		for _, s := range stones {
			chainOf[s] = len(chains)
		}
		chains = append(chains, stones)
	}

	// 2. Player-enclosed regions: maximal connected sets containing no player stone.
	type region struct {
		empties  []int
		bordered map[int]bool
	}
	var regions []region
	seen := make([]bool, len(b.cells))
	for start := range b.cells {
		if seen[start] || (b.cells[start] != Empty && b.cells[start] != opponent) {
			continue
		}
		r := region{bordered: map[int]bool{}}
		stack := []int{start}
		seen[start] = true
		for len(stack) > 0 {
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if b.cells[current] == Empty {
				r.empties = append(r.empties, current)
			}
			for _, n := range b.neighbours(current) {
				if b.cells[n] == player {
					r.bordered[chainOf[n]] = true
				} else if !seen[n] && (b.cells[n] == Empty || b.cells[n] == opponent) {
					seen[n] = true
					stack = append(stack, n)
				}
			}
		}
		regions = append(regions, r)
	}

	// 3. A region is vital to a chain when every empty point in it is a liberty of
	//    that chain -- that is what makes it an eye the opponent cannot fill.
	vital := make([]map[int]bool, len(chains))
	for i := range vital {
		vital[i] = map[int]bool{}
	}
	for regionIndex, r := range regions {
		for chainIndex := range r.bordered {
			isVital := true
			for _, empty := range r.empties {
				touches := false
				for _, n := range b.neighbours(empty) {
					if b.cells[n] == player && chainOf[n] == chainIndex {
						touches = true
						break
					}
				}
				if !touches {
					isVital = false
					break
				}
			}
			if isVital {
				vital[chainIndex][regionIndex] = true
			}
		}
	}

	// 4. Alternately drop chains with fewer than two vital regions and regions that
	//    border a dropped chain, until the sets stop shrinking.
	liveChains := make(map[int]bool, len(chains))
	for i := range chains {
		liveChains[i] = true
	}
	liveRegions := make(map[int]bool, len(regions))
	for i := range regions {
		liveRegions[i] = true
	}

	for changed := true; changed; {
		changed = false
		for chainIndex := range liveChains {
			count := 0
			for regionIndex := range vital[chainIndex] {
				if liveRegions[regionIndex] {
					count++
				}
			}
			if count < 2 {
				delete(liveChains, chainIndex)
				changed = true
			}
		}
		for regionIndex := range liveRegions {
			for chainIndex := range regions[regionIndex].bordered {
				if !liveChains[chainIndex] {
					delete(liveRegions, regionIndex)
					changed = true
					break
				}
			}
		}
	}

	var out [][]Point
	for chainIndex := range liveChains {
		points := make([]Point, len(chains[chainIndex]))
		for i, index := range chains[chainIndex] {
			points[i] = b.pointAt(index)
		}
		out = append(out, points)
	}
	return out
}
