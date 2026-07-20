package sse

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Writer serialises Message values and comment lines to an io.Writer using the
// SSE wire format (§9.2.5).
//
// A Writer is not safe for concurrent use: it reuses an internal buffer between
// calls.
type Writer struct {
	w   io.Writer
	buf bytes.Buffer
}

// NewWriter returns a Writer that encodes SSE events to w. It panics if w is nil.
func NewWriter(w io.Writer) *Writer {
	if w == nil {
		panic("sse: writer cannot be nil")
	}
	return &Writer{w: w}
}

// Write encodes msg as one SSE event frame and writes it with a single Write
// call to the underlying writer.
//
// The frame is a sequence of "name: value" lines terminated by a blank line
// that triggers dispatch on the receiver (§9.2.6). A single "data:" line is
// always emitted — even for empty Data — so the event is dispatched; an event
// carrying only a type is therefore expressible.
//
// Write returns an error, and writes nothing, if any field is not valid UTF-8,
// if ID contains CR, LF, or NUL, or if Event contains CR or LF. Those inputs
// would violate the event-stream encoding, corrupt the framing, or be dropped
// by a conforming receiver, so they are rejected rather than silently altered.
func (w *Writer) Write(msg Message) error {
	if !utf8.ValidString(msg.ID) {
		return errors.New("sse: message ID is not valid UTF-8")
	}
	if strings.ContainsAny(msg.ID, "\r\n\x00") {
		return fmt.Errorf("sse: message ID contains CR, LF, or NUL: %q", msg.ID)
	}
	if !utf8.ValidString(msg.Event) {
		return errors.New("sse: event type is not valid UTF-8")
	}
	if strings.ContainsAny(msg.Event, "\r\n") {
		return fmt.Errorf("sse: event type contains CR or LF: %q", msg.Event)
	}
	if !utf8.Valid(msg.Data) {
		return errors.New("sse: message data is not valid UTF-8")
	}

	w.buf.Reset()
	if msg.ID != "" {
		writeField(&w.buf, fieldID, msg.ID)
	}
	if msg.Event != "" {
		writeField(&w.buf, fieldEvent, msg.Event)
	}
	writeData(&w.buf, msg.Data)
	w.buf.WriteString(lf)

	return w.writeFrame(w.buf.Bytes())
}

// Retry writes a standalone control frame that sets the stream's reconnection
// time (§9.2.6) without dispatching an event, e.g. "retry: 5000\n\n". A negative
// delay is an error; a positive delay below one millisecond is rounded up to
// 1 ms so it never serialises to an immediate-reconnect "retry: 0".
func (w *Writer) Retry(delay time.Duration) error {
	if delay < 0 {
		return fmt.Errorf("sse: retry delay must not be negative: %v", delay)
	}
	w.buf.Reset()
	writeField(&w.buf, fieldRetry, strconv.FormatInt(ceilMillis(delay), 10))
	w.buf.WriteString(lf)
	return w.writeFrame(w.buf.Bytes())
}

// ResetID writes a standalone "id:\n\n" control frame, resetting the receiver's
// last event ID to the empty string (§9.2.6) without dispatching an event. Use
// it because an empty Message.ID omits the id line rather than resetting it.
func (w *Writer) ResetID() error {
	w.buf.Reset()
	w.buf.WriteString(fieldID)
	w.buf.WriteString(colon)
	w.buf.WriteString(lf)
	w.buf.WriteString(lf)
	return w.writeFrame(w.buf.Bytes())
}

// writeFrame writes a complete, already-encoded frame to the underlying writer,
// treating a short write as an error even if the writer misreports it.
func (w *Writer) writeFrame(p []byte) error {
	n, err := w.w.Write(p)
	if err != nil {
		return err
	}
	if n != len(p) {
		return io.ErrShortWrite
	}
	return nil
}

// ceilMillis returns d in whole milliseconds, rounding a positive sub-millisecond
// value up to 1 so a nonzero duration never serialises to zero. A non-positive
// duration returns 0. Division is done before any addition so a duration near
// math.MaxInt64 cannot overflow into a negative result.
func ceilMillis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	ms := d / time.Millisecond
	if d%time.Millisecond != 0 {
		ms++
	}
	return int64(ms)
}

// Comment writes an SSE comment (§9.2.5): a line beginning with ':'. Comments
// are ignored by receivers and dispatch nothing; sending one roughly every 15
// seconds keeps idle proxies from closing the connection (§9.2.7). An empty
// string writes a bare ":" line, the minimal heartbeat. A multi-line comment is
// split into one ":" line per line so it cannot corrupt the framing.
func (w *Writer) Comment(comment string) error {
	if !utf8.ValidString(comment) {
		return errors.New("sse: comment is not valid UTF-8")
	}
	w.buf.Reset()
	writeComment(&w.buf, comment)
	return w.writeFrame(w.buf.Bytes())
}

// writeField appends one "field: value" line. value is known to contain no
// CR/LF (id/event are validated by Message; retry is decimal digits), so it is
// written verbatim.
func writeField(buf *bytes.Buffer, field, value string) {
	buf.WriteString(field)
	buf.WriteString(colon)
	buf.WriteString(space)
	buf.WriteString(value)
	buf.WriteString(lf)
}

// writeData emits one "data" line per logical line in data, splitting on any SSE
// line ending (CRLF, CR, LF) inline — equivalent to running splitLine but with
// no per-call scanner allocation. When data ends on a terminator, or is empty,
// a trailing empty "data:" line is emitted so the receiver reassembles the
// payload exactly and always dispatches the event.
func writeData(buf *bytes.Buffer, data []byte) {
	if len(data) == 0 {
		writeDataLine(buf, nil)
		return
	}
	for {
		i := bytes.IndexAny(data, "\r\n")
		if i < 0 {
			writeDataLine(buf, data)
			return
		}
		writeDataLine(buf, data[:i])
		if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
			data = data[i+2:]
		} else {
			data = data[i+1:]
		}
		if len(data) == 0 {
			writeDataLine(buf, nil)
			return
		}
	}
}

// writeDataLine appends one "data" line. A space separator is added only when
// the value is non-empty, so an empty line is "data:" rather than "data: ".
func writeDataLine(buf *bytes.Buffer, value []byte) {
	buf.WriteString(fieldData)
	buf.WriteString(colon)
	if len(value) > 0 {
		buf.WriteString(space)
		buf.Write(value)
	}
	buf.WriteString(lf)
}

// writeComment emits comment as one or more ":" lines, splitting on any SSE line
// ending so an embedded newline cannot break the framing.
func writeComment(buf *bytes.Buffer, comment string) {
	for {
		i := strings.IndexAny(comment, "\r\n")
		if i < 0 {
			writeCommentLine(buf, comment)
			return
		}
		writeCommentLine(buf, comment[:i])
		if comment[i] == '\r' && i+1 < len(comment) && comment[i+1] == '\n' {
			comment = comment[i+2:]
		} else {
			comment = comment[i+1:]
		}
		if comment == "" {
			writeCommentLine(buf, "")
			return
		}
	}
}

// writeCommentLine appends one ":" line, adding a space separator only when the
// text is non-empty so an empty comment is a bare ":".
func writeCommentLine(buf *bytes.Buffer, text string) {
	buf.WriteString(colon)
	if text != "" {
		buf.WriteString(space)
		buf.WriteString(text)
	}
	buf.WriteString(lf)
}
