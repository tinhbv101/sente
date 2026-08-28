package rules

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Cross-engine differential trace.
//
// The parity lock compares final positions; this compares every move of full
// games recorded by the other engine. A divergence anywhere in a long game is
// caught at the exact move it happens, which is what makes ADR-002 a check rather
// than an intention.

type gameTrace struct {
	RulesVersion string `json:"rulesVersion"`
	Note         string `json:"note"`
	Games        []struct {
		ID         string         `json:"id"`
		Size       int            `json:"size"`
		Rules      string         `json:"rules"`
		Moves      []string       `json:"moves"`
		Hashes     []string       `json:"hashes"`
		Captures   map[string]int `json:"captures"`
		FinalPhase string         `json:"finalPhase"`
	} `json:"games"`
}

func TestReplayingTheGameTraceReproducesEveryHash(t *testing.T) {
	path := filepath.Join(specRoot(t), "parity", "games.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var trace gameTrace
	if err := json.Unmarshal(data, &trace); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	if len(trace.Games) == 0 {
		t.Fatal("game trace is empty")
	}

	checkedMoves := 0
	for _, game := range trace.Games {
		game := game
		t.Run(game.ID, func(t *testing.T) {
			ruleSet := Japanese
			if game.Rules == "chinese" {
				ruleSet = Chinese
			}
			engine := NewGame(game.Size, ruleSet, DefaultKomi(ruleSet, 0), 0)

			for index, encoded := range game.Moves {
				var move Move
				switch encoded {
				case "pass":
					move = Pass
				case "resign":
					move = Resign
				default:
					p, ok := ParseCoordinate(encoded, game.Size)
					if !ok {
						t.Fatalf("bad coordinate %q at move %d", encoded, index+1)
					}
					move = Play(p)
				}
				next, err := engine.Apply(move)
				if err != nil {
					t.Fatalf("move %d (%s) rejected: %v", index+1, encoded, err)
				}
				engine = next
				if got := fmt.Sprintf("0x%016X", engine.State.BoardHash); got != game.Hashes[index] {
					t.Fatalf("hash diverged at move %d (%s): want %s, got %s",
						index+1, encoded, game.Hashes[index], got)
				}
				checkedMoves++
			}

			if engine.State.Captures.Black != game.Captures["black"] {
				t.Errorf("black prisoners: want %d, got %d", game.Captures["black"], engine.State.Captures.Black)
			}
			if engine.State.Captures.White != game.Captures["white"] {
				t.Errorf("white prisoners: want %d, got %d", game.Captures["white"], engine.State.Captures.White)
			}
			if string(engine.State.Phase) != game.FinalPhase {
				t.Errorf("final phase: want %s, got %s", game.FinalPhase, engine.State.Phase)
			}
		})
	}
	t.Logf("replayed %d games, %d move-by-move hash checks against rules_version %s",
		len(trace.Games), checkedMoves, trace.RulesVersion)
}
