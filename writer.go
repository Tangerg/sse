package sse

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// flushWriter wraps an [http.ResponseWriter] and calls Flush after every Write,
// ensuring each SSE event frame reaches the client without buffering delay.
// SSE connections are long-lived, so per-event flushing is essential.
type flushWriter struct {
	rw      http.ResponseWriter
	flusher http.Flusher
}

var _ io.Writer = (*flushWriter)(nil)

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.rw.Write(p)
	if err != nil {
		return n, err
	}
	f.flusher.Flush()
	return n, nil
}

// setSSEHeaders writes the HTTP response headers required for an SSE endpoint.
//
//   - Content-Type: text/event-stream — mandated by §9.2.5 ("This event stream
//     format's MIME type is text/event-stream").
//   - Connection: keep-alive — SSE relies on a persistent connection.
//   - Cache-Control: no-cache — set only when the caller has not already
//     supplied a Cache-Control value, preventing intermediaries from buffering
//     the live stream.
func setSSEHeaders(header http.Header) {
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Connection", "keep-alive")

	if header.Get("Cache-Control") == "" {
		header.Set("Cache-Control", "no-cache")
	}
}

// Writer serialises [Message] values and comment lines to an [io.Writer] using
// the SSE wire format defined in §9.2.5.
type Writer struct {
	w io.Writer
}

// NewWriter creates a [Writer] that encodes SSE events to w.
func NewWriter(w io.Writer) (*Writer, error) {
	if w == nil {
		return nil, errors.New("sse: writer cannot be nil")
	}
	return &Writer{w: w}, nil
}

// NewHTTPWriter creates a [Writer] for an HTTP response. It sets the required
// SSE headers via [setSSEHeaders] and wraps rw in a [flushWriter] so each
// event is flushed to the client immediately.
//
// rw must implement [http.Flusher]; the standard library's ResponseWriter
// always does, but test doubles or middleware wrappers may not.
func NewHTTPWriter(rw http.ResponseWriter) (*Writer, error) {
	if rw == nil {
		return nil, errors.New("sse: http.ResponseWriter cannot be nil")
	}

	flusher, ok := rw.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("sse: %T does not implement http.Flusher; streaming is not supported", rw)
	}

	setSSEHeaders(rw.Header())

	return NewWriter(&flushWriter{
		rw:      rw,
		flusher: flusher,
	})
}

// fieldBuf is an in-memory buffer for building a single SSE event frame before
// writing it atomically to the underlying writer.
type fieldBuf struct {
	*bytes.Buffer
}

func newFieldBuf(capacity int) *fieldBuf {
	return &fieldBuf{bytes.NewBuffer(make([]byte, 0, capacity))}
}

// newlineStripper removes CR and LF characters from field values before they
// are written to the wire.
//
// The §9.2.5 ABNF restricts field values to any-char, which explicitly
// excludes U+000A LF and U+000D CR:
//
//	any-char = %x0000-0009 / %x000B-000C / %x000E-10FFFF
//
// Embedding raw newlines would corrupt the framing by introducing spurious
// field boundaries or blank-line dispatch triggers.
var newlineStripper = strings.NewReplacer("\r\n", "", "\r", "", "\n", "")

// write appends one SSE line in the form "field: value\n" (§9.2.5 field rule).
// When field is empty the line is a comment (": value\n", §9.2.5 comment rule).
func (b *fieldBuf) write(field, value string) {
	if field != "" {
		b.WriteString(field)
	}
	b.WriteString(colon)
	b.WriteString(space)
	b.WriteString(newlineStripper.Replace(value))
	b.WriteString(lf)
}

// writeID writes an "id" field line. An empty id is omitted; the receiver will
// then inherit the last event ID from a previous event (§9.2.6 dispatch step 1).
func (b *fieldBuf) writeID(id string) {
	if len(id) == 0 {
		return
	}
	b.write(fieldID, id)
}

// writeEvent writes an "event" field line. An empty event type is omitted; the
// receiver will default the event type to "message" on dispatch (§9.2.6
// dispatch step 4).
func (b *fieldBuf) writeEvent(event string) {
	if len(event) == 0 {
		return
	}
	b.write(fieldEvent, event)
}

// writeData encodes the payload as one "data" field line per logical line
// (§9.2.6: "Append the field value to the data buffer, then append a single
// U+000A LINE FEED (LF) character to the data buffer.").
//
// Multi-line values are split using [splitLine] so that all three SSE line
// endings are handled correctly. When data ends with a line terminator an
// extra empty "data:" line is appended, because the scanner never emits a
// trailing empty token — without it the receiver would lose the trailing
// newline after reassembly.
//
// An empty data slice is omitted entirely; the resulting event will be
// discarded by the receiver per §9.2.6 dispatch step 2.
func (b *fieldBuf) writeData(data []byte) {
	if len(data) == 0 {
		return
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Split(splitLine)
	for scanner.Scan() {
		b.write(fieldData, scanner.Text())
	}

	// splitLine returns (0, nil, nil) when atEOF and the buffer is empty,
	// so the scanner never emits a trailing empty token. Write an explicit
	// empty data line when the payload ends with a line terminator.
	if last := data[len(data)-1]; last == lf[0] || last == cr[0] {
		b.write(fieldData, "")
	}
}

// writeRetry writes a "retry" field line with the duration converted to
// milliseconds (§9.2.6: "interpret the field value as an integer in base ten").
// A zero or negative duration is omitted.
func (b *fieldBuf) writeRetry(retry time.Duration) {
	if retry <= 0 {
		return
	}
	b.write(fieldRetry, strconv.FormatInt(retry.Milliseconds(), 10))
}

// writeComment writes an SSE comment line (§9.2.5: "comment = colon *any-char
// end-of-line"). An empty comment writes a bare colon line (":\n") so that
// sw.Comment(ctx, "") is a valid spec-compliant heartbeat; a non-empty comment
// adds a space separator (": value\n").
func (b *fieldBuf) writeComment(comment string) {
	b.WriteString(colon)
	if comment != "" {
		b.WriteString(space)
		b.WriteString(newlineStripper.Replace(comment))
	}
	b.WriteString(lf)
}

// Message encodes msg as a complete SSE event frame and writes it atomically
// to the underlying writer.
//
// The frame follows the §9.2.5 field grammar: each non-empty field is written
// as "name: value\n", and the frame is terminated with a blank line ("\n")
// that signals the receiver to dispatch the event (§9.2.6).
//
// Fields with zero values are omitted:
//   - Empty ID omits the "id" line (receiver keeps the previous last-event-ID).
//   - Empty Event omits the "event" line (receiver defaults to "message").
//   - Empty Data omits all "data" lines (receiver discards the event).
//   - Zero/negative Retry omits the "retry" line.
//
// Returns ctx.Err() immediately if the context is already done.
func (w *Writer) Message(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	buf := newFieldBuf(len(msg.ID) + len(msg.Event) + 2*len(msg.Data) + 8)

	buf.writeID(msg.ID)
	buf.writeEvent(msg.Event)
	buf.writeData(msg.Data)
	buf.writeRetry(msg.Retry)
	buf.WriteString(lf)

	_, err := w.w.Write(buf.Bytes())
	return err
}

// Comment encodes comment as an SSE comment line and writes it to the
// underlying writer.
//
// Per §9.2.5, a comment is any line beginning with ':':
//
//	comment = colon *any-char end-of-line
//
// Comment lines are ignored by the receiver and never dispatch an event.
// Per §9.2.7, sending a comment roughly every 15 seconds prevents idle
// proxy servers from closing the connection. An empty string is valid and
// sufficient — it writes a bare colon line with no text (":\n"), which
// conforming receivers treat as a comment:
//
//	sw.Comment(ctx, "")
//
// The frame is terminated with a blank line, so the wire representation of
// an empty comment is ":\n\n".
//
// Returns ctx.Err() immediately if the context is already done.
func (w *Writer) Comment(ctx context.Context, comment string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	buf := newFieldBuf(len(comment) + 4)
	buf.writeComment(comment)
	buf.WriteString(lf)

	_, err := w.w.Write(buf.Bytes())
	return err
}
