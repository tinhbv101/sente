package httpapi

import "net/http"

// English texts per error code, for clients that ask via Accept-Language.
// Vietnamese stays the source of truth at every call site; this table only
// needs the codes a person can actually see. Unknown codes fall back to the
// Vietnamese message rather than to silence.
var englishErrorText = map[string]string{
	"internal":               "Something went wrong. Please try again.",
	"malformed":              "The request was not understood.",
	"unauthorized":           "Your session has expired.",
	"session_revoked":        "Your session was revoked. Please sign in again.",
	"not_found":              "Not found.",
	"challenge_gone":         "This invitation is no longer valid.",
	"own_challenge":          "This is your own invitation.",
	"invalid_config":         "These game settings are not allowed.",
	"invalid_name":           "The name must be 2 to 24 characters.",
	"invalid_device":         "The device details are not valid.",
	"invalid_identity_token": "Apple could not verify this sign-in.",
	"invalid_payload":        "The notification payload is not valid.",
	"invalid_report":         "The report is not valid.",
	"invalid_block":          "The block request is not valid.",
	"apple_required":         "Sign in with Apple to add friends.",
	"invite_refused":         "This player cannot be invited.",
	"identity_taken":         "This Apple ID was just used elsewhere. Try again.",
	"not_available":          "This feature is not enabled on this server.",
	"not_configured":         "Universal links are not configured.",
	"unavailable":            "The server is not ready. Please try again.",
	"rate_limited":           "Too many requests. Please slow down.",
	"protocol_unsupported":   "This app version is too old for the server.",
}

// The WebSocket move verdicts, mirroring messageForCode.
var englishWSText = map[string]string{
	"occupied":         "That point is occupied.",
	"suicide":          "That move is suicide.",
	"ko":               "Forbidden by the ko rule.",
	"superko":          "That move repeats an earlier position.",
	"not_your_turn":    "It is not your turn.",
	"out_of_bounds":    "That point is off the board.",
	"out_of_sync":      "The board changed. Resyncing…",
	"game_not_playing": "The game is over.",
}

// wantsEnglish mirrors the privacy page's language pick.
func wantsEnglish(r *http.Request) bool { return pickLanguage(r) == "en" }

func errorText(r *http.Request, code, vietnamese string) string {
	if r != nil && wantsEnglish(r) {
		if english, ok := englishErrorText[code]; ok {
			return english
		}
	}
	return vietnamese
}
