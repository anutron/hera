package mcp

import "github.com/anutron/hera/internal/argus"

// LinkGate is the shared preamble every hera_* tool handler (except
// hera_status) runs before its body. When the argus link is not healthy,
// LinkGate returns a structured tool error and ok=true; the caller must
// return that response immediately and skip the normal body. When the
// link is healthy, ok=false and the caller proceeds.
//
// hera_status is intentionally NOT gated: callers must always be able to
// ask hera for the current link state. Instead, hera_status surfaces the
// link state in its response payload (argus_link / argus_link_error).
func LinkGate() (Response, bool) {
	switch argus.GetLinkState() {
	case argus.LinkRecovering:
		return ErrorResponse("argus link recovering, retry in a moment"), true
	case argus.LinkDown:
		msg := "argus link down"
		if err := argus.LinkLastError(); err != nil {
			msg += ": " + err.Error()
		}
		return ErrorResponse(msg), true
	default:
		return Response{}, false
	}
}
