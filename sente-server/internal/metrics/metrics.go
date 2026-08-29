// Package metrics holds the four series docs/03 ADR-015 says must exist from day
// one. Everything else can wait; these are the ones that tell you the two rules
// engines have drifted or that games are breaking.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// MoveApplyDuration guards NFR-P2 (p95 < 30ms).
	MoveApplyDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "sente_move_apply_seconds",
		Help:    "Time from a move command arriving at its actor to the events being ready.",
		Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1},
	})

	// IllegalMoveRejected is the engine-drift alarm. The client already refuses
	// illegal moves locally, so a server rejection for a *rules* reason
	// (occupied, suicide, ko, superko) means the two engines disagree. Any
	// non-zero rate on those labels is a P1 (docs/04 §9.2).
	IllegalMoveRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sente_illegal_move_rejected_total",
		Help: "Moves the server refused, by reason code.",
	}, []string{"reason"})

	// GameEnded feeds the abandonment rate and the product dashboard.
	GameEnded = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sente_game_ended_total",
		Help: "Games finished, by reason.",
	}, []string{"reason"})

	// GameDesync counts clients that had to resync because their hash disagreed
	// with the server's -- the other half of the drift alarm.
	GameDesync = promauto.NewCounter(prometheus.CounterOpts{
		Name: "sente_game_desync_total",
		Help: "Client-reported board hash mismatches.",
	})

	// OwnerReclaimed shows nodes coming and going. A spike means a node is flapping.
	OwnerReclaimed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "sente_owner_reclaimed_total",
		Help: "Games taken over from a node that stopped without saying so.",
	})
)
