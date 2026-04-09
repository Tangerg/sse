// Package sse implements reading and writing of Server-Sent Events (SSE)
// streams as defined by the WHATWG HTML Living Standard §9.2.
// https://html.spec.whatwg.org/multipage/server-sent-events.html
//
// # Stream format (§9.2.5)
//
// An SSE stream is a UTF-8 encoded sequence of events separated by blank
// lines. Each event is a sequence of field lines followed by one blank line
// that triggers dispatch. The ABNF grammar is:
//
//	stream      = [ bom ] *event
//	event       = *( comment / field ) end-of-line
//	comment     = colon *any-char end-of-line
//	field       = 1*name-char [ colon [ space ] *any-char ] end-of-line
//	end-of-line = ( cr lf / cr / lf )
//
// Four field names are defined: "data", "event", "id", and "retry".
// Any other field name is silently ignored.
//
// # Parsing (§9.2.6)
//
// The stream is decoded as UTF-8; a leading BOM (U+FEFF) is stripped before
// parsing begins. Lines are then processed one by one:
//
//   - Blank line: dispatch the current event (see below).
//   - Starts with ':': comment line, ignored entirely.
//   - Contains ':': split at the first colon; strip one leading space from the
//     value if present; process as a named field.
//   - No ':': treat the entire line as the field name with an empty value.
//
// Field processing rules:
//
//   - "event" – set the event-type buffer to the field value.
//   - "data"  – append the field value followed by U+000A LF to the data buffer.
//   - "id"    – if the value contains no U+0000 NULL, update the last-event-ID
//     buffer; otherwise ignore. The buffer is never reset between events —
//     it persists until the server explicitly clears it.
//   - "retry" – if the value consists solely of ASCII digits, set the
//     reconnection time (in ms); otherwise ignore the field.
//
// Dispatch algorithm (blank line):
//
//  1. Copy the last-event-ID buffer into the event's ID (it is not cleared).
//  2. If the data buffer is empty, clear the event-type buffer and return
//     without firing — events with no data are discarded.
//  3. Strip the trailing U+000A LF that was appended after the last data line.
//  4. Set the event type to the event-type buffer value, or "message" if empty.
//  5. Clear the data buffer and the event-type buffer.
//
// Any data accumulated when the stream ends without a final blank line is
// discarded (§9.2.6: "If the file ends in the middle of an event, before the
// final empty line, the incomplete event is not dispatched.").
//
// # Writing events
//
// Use [NewHTTPWriter] inside an HTTP handler to obtain a [Writer] that sets
// the required response headers and flushes each event to the client
// immediately:
//
//	func handler(w http.ResponseWriter, r *http.Request) {
//	    sw, err := sse.NewHTTPWriter(w)
//	    if err != nil {
//	        http.Error(w, err.Error(), http.StatusInternalServerError)
//	        return
//	    }
//	    sw.Message(r.Context(), sse.Message{Event: "update", Data: []byte("hello")})
//	    sw.Comment(r.Context(), "keep-alive") // heartbeat
//	}
//
// For non-HTTP destinations use [NewWriter]:
//
//	sw, err := sse.NewWriter(w)
//
// # Reading events
//
// Use [NewHTTPReader] to consume an SSE response:
//
//	resp, _ := http.Get(url)
//	defer resp.Body.Close()
//
//	sr, err := sse.NewHTTPReader(resp)
//	for msg, err := range sr.Messages(ctx) {
//	    if err != nil { ... }
//	    fmt.Println(msg.Event, string(msg.Data))
//	}
//
// The error value is non-nil only on context cancellation or an I/O error;
// normal end-of-stream is not reported as an error — the loop simply ends.
//
// Context cancellation is cooperative (checked between scans). To unblock a
// scan waiting on a stalled connection, close the underlying reader:
//
//	resp.Body.Close()
//
// For non-HTTP sources use [NewReader]. Both constructors accept an optional
// buffer-size argument (in bytes) that overrides the default 64 KiB per-line
// scanner limit:
//
//	sr, err := sse.NewReader(r, 1<<20) // 1 MiB limit
//
// # Heartbeats (§9.2.7)
//
// Proxy servers may drop idle HTTP connections after a short timeout.
// The spec recommends sending a comment line roughly every 15 seconds.
// An empty string is a valid comment and produces the minimal wire frame
// ":\n\n" (bare colon line followed by a blank line):
//
//	sw.Comment(ctx, "")
package sse
