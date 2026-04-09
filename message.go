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
	null  = "\x00"
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
// dispatch step 4: "… or 'message' if the event type buffer is empty").
const defaultEvent = "message"

// Message represents a single dispatched SSE event.
//
// Field semantics follow §9.2.6 exactly:
//
//   - ID    corresponds to the last-event-ID buffer. It persists across events
//     until the server clears it by sending an empty "id" field. Values
//     containing U+0000 NULL are never stored (the field is ignored on receipt).
//
//   - Event is the event-type buffer value. When absent from the stream the
//     field defaults to "message" on dispatch. When writing, an empty Event
//     omits the "event" line from the wire frame.
//
//   - Data is the payload assembled from one or more "data" field lines.
//     The parser appends U+000A LF after each line, then strips the trailing
//     LF at dispatch. Multi-line values are joined with a single LF. An event
//     with an empty data buffer is discarded without dispatch.
//
//   - Retry carries the reconnection-time hint from the "retry" field,
//     converted from milliseconds. When writing, a zero or negative value
//     omits the "retry" line from the wire frame.
type Message struct {
	ID    string
	Event string
	Data  []byte
	Retry time.Duration
}
