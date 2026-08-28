package store

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"time"
)

// NewID returns a UUID v7: 48 bits of Unix milliseconds followed by random bits.
//
// Time-ordered ids keep inserts at the right-hand edge of the primary key index
// instead of scattering across it, and they make `moves` rows for one game land
// near each other on disk (docs/05 §2). Hand-rolled to avoid a dependency for
// twenty lines.
func NewID() string { return newIDAt(time.Now()) }

func newIDAt(now time.Time) string {
	var id [16]byte
	binary.BigEndian.PutUint64(id[:8], uint64(now.UnixMilli())<<16)
	if _, err := rand.Read(id[6:]); err != nil {
		panic("store: no entropy available for id generation: " + err.Error())
	}
	id[6] = (id[6] & 0x0F) | 0x70 // version 7
	id[8] = (id[8] & 0x3F) | 0x80 // RFC 4122 variant
	return format(id)
}

func format(id [16]byte) string {
	hexed := make([]byte, 32)
	hex.Encode(hexed, id[:])
	out := make([]byte, 0, 36)
	for _, span := range [][2]int{{0, 8}, {8, 12}, {12, 16}, {16, 20}, {20, 32}} {
		if len(out) > 0 {
			out = append(out, '-')
		}
		out = append(out, hexed[span[0]:span[1]]...)
	}
	return string(out)
}

// friendCodeAlphabet omits I, O, 0 and 1 so a code can be read out over the phone
// without ambiguity. Exactly 32 symbols, so an 8-character code carries 40 bits --
// far too large to enumerate at ten lookups a minute (docs/08 §4.2).
const friendCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

const friendCodeLength = 8

// NewFriendCode returns a code for a new account. Callers must handle a unique
// violation by retrying: at 40 bits a collision is vanishingly rare, but the
// database constraint is what actually guarantees uniqueness.
func NewFriendCode() string {
	raw := make([]byte, friendCodeLength)
	if _, err := rand.Read(raw); err != nil {
		panic("store: no entropy available for friend code generation: " + err.Error())
	}
	code := make([]byte, friendCodeLength)
	for i, b := range raw {
		code[i] = friendCodeAlphabet[int(b)%len(friendCodeAlphabet)]
	}
	return string(code)
}
