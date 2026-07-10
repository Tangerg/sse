// Package sse reads and writes Server-Sent Events (SSE) streams as defined by
// the WHATWG HTML Living Standard §9.2.
// https://html.spec.whatwg.org/multipage/server-sent-events.html
//
// The package covers the core parsing and serialisation rules of §9.2: BOM
// stripping, all three line endings (LF, CR, CRLF), the four fields (data,
// event, id, retry), the single-leading-space rule, id/retry validation, and
// blank-line dispatch. It does not implement the full HTML EventSource client
// (automatic reconnection, request retries, or the UTF-8 decode algorithm's
// replacement of malformed sequences with U+FFFD — bytes pass through
// unchanged), which are the caller's responsibility.
//
// # Reading
//
// A [Reader] yields one [Message] per event. Messages returns a
// range-over-func iterator; a clean end of stream ends the loop without an
// error.
//
//	resp, _ := http.Get(url)
//	defer resp.Body.Close()
//
//	r, err := sse.NewHTTPReader(resp)
//	if err != nil { ... }
//	for msg, err := range r.Messages() {
//	    if err != nil { ... }
//	    fmt.Println(msg.Event, string(msg.Data))
//	}
//
// [NewHTTPReader] validates the response media type; for any other source use
// [NewReader]. There is no context parameter: to interrupt a read blocked on a
// stalled connection, close the underlying reader; to stop early, break the
// loop. When a single data field may exceed 64 KiB (e.g. a large JSON payload),
// raise [Reader.MaxLineBytes] before the first call to Messages; for untrusted
// input, set [Reader.MaxEventBytes] to bound the total size of one event.
//
// After the loop ends, [Reader.LastEventID] and [Reader.Retry] report the
// reconnection state needed to reconnect — including values from standalone
// "id:" or "retry:" frames that dispatched no event.
//
// # Writing
//
// A [Writer] serialises events. [NewHTTPWriter] sets the SSE response headers
// and flushes each frame to the client:
//
//	func handler(w http.ResponseWriter, r *http.Request) {
//	    sw, err := sse.NewHTTPWriter(w)
//	    if err != nil {
//	        http.Error(w, err.Error(), http.StatusInternalServerError)
//	        return
//	    }
//	    sw.Message(sse.Message{Event: "update", Data: []byte("hello")})
//	    sw.Comment("keep-alive") // heartbeat (§9.2.7)
//	}
//
// Every [Writer.Message] call emits a dispatchable event, including a "data:"
// line even when Data is empty, so an event carrying only a type is
// expressible. An ID containing CR, LF, or NUL, or an event type containing CR
// or LF, is rejected with an error rather than silently altered. For non-HTTP
// destinations use [NewWriter].
//
// [Writer.Retry] and [Writer.ResetID] emit standalone control frames — a new
// reconnection time or a reset of the last event ID — without dispatching an
// event.
//
// # Retry
//
// Per §9.2.6 the retry (reconnection) time is stream-level state, not a
// per-event field: once a stream sets it, [Message.Retry] reports the same value
// on every subsequent event until the stream changes it. Writing a positive
// [Message.Retry] emits a retry line that the receiver keeps until updated.
package sse
