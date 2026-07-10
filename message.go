package sse

import "time"

// Wire-protocol constants derived from the ABNF in §9.2.5.
//
//	end-of-line = ( cr lf / cr / lf )
//	lf          = %x000A ; U+000A LINE FEED
//	cr          = %x000D ; U+000D CARRIAGE RETURN
//	space       = %x0020 ; U+0020 SPACE
//	colon       = %x003A ; U+003A COLON
//	bom         = %xFEFF ; U+FEFF BYTE ORDER MARK
const (
	lf    = "\n"
	cr    = "\r"
	space = " "
	colon = ":"
	bom   = "\uFEFF"
)

// The four field names recognised by the parsing algorithm (§9.2.6).
// Any other field name is silently ignored.
const (
	fieldID    = "id"
	fieldEvent = "event"
	fieldData  = "data"
	fieldRetry = "retry"
)

// defaultEvent is the event type used when the "event" field is absent (§9.2.6
// dispatch step: "… or 'message' if the event type buffer is empty").
const defaultEvent = "message"

// Message is a single SSE event, used both as the value produced when reading a
// stream and as the value supplied when writing one.
//
// Field semantics follow §9.2.6:
//
//   - ID is the last-event-ID. On read it persists across events until the
//     server sends a new value; dispatch does not clear it (an id field, even an
//     empty one, is what changes it). Received values containing U+0000 NULL are
//     ignored. On write, a non-empty ID emits an "id" line; an empty ID omits
//     the line, so the receiver keeps the previous value (use [Writer.ResetID]
//     to reset it). Writing an ID that contains CR, LF, or NUL is an error.
//
//   - Event is the event type. On read it defaults to "message" when the stream
//     omits it. On write, a non-empty Event emits an "event" line; an empty
//     Event omits it. Writing an Event that contains CR or LF is an error.
//
//   - Data is the payload. On read, multiple "data" lines are joined with LF and
//     the trailing LF is stripped. On write, Data is split on any line ending
//     into one "data" line each; an empty Data still emits a single "data:" line
//     so the event is dispatched (an event carrying only a type, e.g. a refresh
//     signal, is therefore expressible).
//
//   - Retry is the stream's reconnection time. Per §9.2.6 it is stream-level
//     state, not per-event: on read it persists across events once set and is
//     reported on every subsequent Message. On write, a positive value emits a
//     "retry" line, rounded up to the nearest millisecond (a positive
//     sub-millisecond value becomes 1 ms); a zero value omits it and a negative
//     value is an error. Use [Writer.Retry] to change the reconnection time
//     without an event.
type Message struct {
	ID    string
	Event string
	Data  []byte
	Retry time.Duration
}
