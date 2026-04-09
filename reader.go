package sse

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// stripBOM implements the first step of the UTF-8 decode algorithm required by
// §9.2.6: "Streams must be decoded using the UTF-8 decode algorithm. The UTF-8
// decode algorithm strips one leading UTF-8 Byte Order Mark (BOM), if any."
//
// It peeks at the first rune via a [bufio.Reader] and unreads it when it is not
// a BOM, so the returned reader always starts at the first non-BOM byte.
func stripBOM(r io.Reader) (io.Reader, error) {
	br := bufio.NewReader(r)

	char, _, err := br.ReadRune()
	if err == io.EOF {
		return br, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sse: reading BOM: %w", err)
	}

	if string(char) != bom {
		if err = br.UnreadRune(); err != nil {
			return nil, fmt.Errorf("sse: unreading BOM character: %w", err)
		}
	}

	return br, nil
}

// newLineScanner strips the BOM from r (§9.2.6) and wraps the result in a
// [bufio.Scanner] configured to split on all three SSE line endings defined in
// §9.2.5 (CRLF, lone CR, lone LF) via [splitLine].
func newLineScanner(r io.Reader) (*bufio.Scanner, error) {
	stripped, err := stripBOM(r)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(stripped)
	scanner.Split(splitLine)

	return scanner, nil
}

// Reader parses an SSE event stream according to §9.2.6, yielding one
// [Message] per blank-line dispatch boundary.
//
// The parser maintains three mutable buffers that survive across dispatch
// boundaries, matching the state machine described by the spec:
//
//   - lastEventID – the last-event-ID buffer; updated by "id" fields and never
//     reset, so its value carries forward to every subsequent event (§9.2.6
//     dispatch step 1).
//   - dataBuf     – the data buffer; accumulated from "data" fields, cleared
//     on each dispatch (§9.2.6 dispatch steps 2–3, 7).
//   - eventType   – the event-type buffer; set by "event" fields, cleared on
//     each dispatch (§9.2.6 dispatch steps 4, 7).
//   - retry       – the reconnection-time hint from the "retry" field, carried
//     in the Message and cleared after each dispatch.
//
// The scanner is created in [Reader.Messages] and captured by the returned
// closure, so that [NewReader] never performs I/O and never returns an error.
type Reader struct {
	r io.Reader

	lastEventID string
	dataBuf     *bytes.Buffer
	eventType   string
	retry       time.Duration
}

// NewReader creates a [Reader] that parses the SSE event stream from r.
// Panics if r is nil.
//
// No I/O is performed during construction; the scanner is initialised on the
// first call to [Reader.Messages].
func NewReader(r io.Reader) *Reader {
	if r == nil {
		panic("sse: reader cannot be nil")
	}

	return &Reader{
		r:       r,
		dataBuf: bytes.NewBuffer(nil),
	}
}

// NewHTTPReader creates a [Reader] from an HTTP response, first verifying that
// the Content-Type header is "text/event-stream" as required by §9.2.5
// ("This event stream format's MIME type is text/event-stream").
func NewHTTPReader(resp *http.Response) (*Reader, error) {
	if resp == nil {
		return nil, errors.New("sse: http.Response cannot be nil")
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		return nil, errors.New("sse: missing Content-Type header")
	}

	if !strings.HasPrefix(contentType, "text/event-stream") {
		return nil, fmt.Errorf("sse: Content-Type must be 'text/event-stream', got %q", contentType)
	}

	return NewReader(resp.Body), nil
}

// parseLine applies the per-line processing rules from §9.2.6 to a single
// non-empty line and updates the reader's field buffers accordingly.
//
// The spec defines four cases:
//
//  1. Line starts with ':' → comment; ignore entirely.
//     ("If the line starts with a U+003A COLON character (:), ignore the line.")
//
//  2. Line contains ':' → split at the first colon; strip one leading space
//     from the value if present; process as a named field.
//     ("If value starts with a U+0020 SPACE character, remove it from value.")
//
//  3. Line contains no ':' → the whole line is the field name; value is "".
//     ("Process the field using the whole line as the field name, and the
//     empty string as the field value.")
//
// Field-specific rules (§9.2.6 "process the field"):
//
//   - "event" → set eventType to value.
//   - "data"  → append value + U+000A LF to dataBuf.
//   - "id"    → if value contains no U+0000 NULL, update lastEventID;
//     otherwise ignore ("If the field value does not contain U+0000 NULL,
//     then set the last event ID buffer to the field value.").
//   - "retry" → if value is all ASCII digits, parse as milliseconds and store;
//     otherwise ignore ("If the field value consists of only ASCII digits…").
//   - anything else → ignore ("The field is ignored.").
func (r *Reader) parseLine(line string) {
	// Case 1: comment line — discard.
	if strings.HasPrefix(line, colon) {
		return
	}

	field, value, found := strings.Cut(line, colon)
	if !found {
		// Case 3: no colon — whole line is field name, value is empty.
		field = line
		value = ""
	} else {
		// Case 2: strip a single leading space from the value (§9.2.6).
		value = strings.TrimPrefix(value, space)
	}

	switch field {
	case fieldID:
		// Ignore id values that contain U+0000 NULL (§9.2.6).
		if !strings.Contains(value, null) {
			r.lastEventID = value
		}

	case fieldEvent:
		r.eventType = value

	case fieldData:
		// Append value then U+000A LF to the data buffer (§9.2.6).
		r.dataBuf.WriteString(value)
		r.dataBuf.WriteString(lf)

	case fieldRetry:
		// Value must consist solely of ASCII digits; ignore otherwise (§9.2.6).
		if len(value) == 0 {
			return
		}
		for _, ch := range value {
			if ch < '0' || ch > '9' {
				return
			}
		}

		ms, err := strconv.Atoi(value)
		if err != nil {
			return
		}
		r.retry = time.Duration(ms) * time.Millisecond
	}
}

// buildMessage runs the dispatch algorithm from §9.2.6 when a blank line is
// encountered, assembling a [Message] from the accumulated field buffers.
//
// Dispatch steps (§9.2.6):
//
//  1. Copy lastEventID into the message (buffer is not cleared).
//  2. If dataBuf is empty, clear eventType and retry, then return (false) —
//     events with no data are discarded without dispatch.
//  3. Strip the trailing U+000A LF appended after the last "data" line.
//     The slice is cloned so it does not alias dataBuf's memory, which is
//     overwritten on the next Reset.
//  4. Set Event to eventType, or "message" if eventType is empty.
//  5. Clear dataBuf and eventType (retry is also cleared here).
func (r *Reader) buildMessage() (Message, bool) {
	// Dispatch step 2: empty data buffer → discard.
	if r.dataBuf.Len() == 0 {
		r.eventType = ""
		r.retry = 0
		return Message{}, false
	}

	// Dispatch step 3: remove the trailing LF appended by the last "data" line.
	data := bytes.Clone(bytes.TrimSuffix(r.dataBuf.Bytes(), []byte(lf)))

	// Dispatch step 4: default event type is "message".
	msg := Message{
		ID:    r.lastEventID,
		Event: defaultEvent,
		Data:  data,
		Retry: r.retry,
	}

	if r.eventType != "" {
		msg.Event = r.eventType
	}

	// Dispatch step 5: clear the data and event-type buffers.
	r.dataBuf.Reset()
	r.eventType = ""
	r.retry = 0

	return msg, true
}

// Messages returns an iterator over all SSE events in the stream.
//
// The iterator drives the §9.2.6 parsing loop: it scans line by line, calling
// [Reader.parseLine] for non-empty lines and [Reader.buildMessage] on blank
// lines. A blank line that does not produce a message (empty data buffer) is
// silently skipped.
//
// The error value yielded alongside each message follows these rules:
//   - Normal end-of-stream (EOF) is never reported as an error — the iterator
//     simply stops yielding.
//   - Context cancellation or deadline expiry yields ctx.Err() as the final
//     value and the iterator returns immediately.
//   - Any I/O or scanner error is yielded as the final value and the iterator
//     stops.
//
// Context cancellation is cooperative: it is checked before every scan, so
// an in-progress [bufio.Scanner.Scan] call is not interrupted mid-read.
// Cancellation takes effect at the next iteration boundary. To unblock a scan
// that is waiting on a stalled connection, close the underlying [io.Reader]
// (e.g. resp.Body.Close() for an HTTP response).
//
// Per §9.2.6, data accumulated at end-of-stream without a trailing blank line
// is discarded — the iterator ends without dispatching an incomplete event.
func (r *Reader) Messages(ctx context.Context) iter.Seq2[Message, error] {
	scanner, err := newLineScanner(r.r)
	return func(yield func(Message, error) bool) {
		if err != nil {
			yield(Message{}, err)
			return
		}

		for {
			if err = ctx.Err(); err != nil {
				yield(Message{}, err)
				return
			}

			if !scanner.Scan() {
				break
			}

			line := scanner.Text()

			if len(line) == 0 {
				// Blank line: attempt to dispatch the current event (§9.2.6).
				msg, ok := r.buildMessage()
				if ok {
					if !yield(msg, nil) {
						return
					}
				}
				continue
			}

			r.parseLine(line)
		}

		if err = scanner.Err(); err != nil {
			yield(Message{}, err)
		}
	}
}
