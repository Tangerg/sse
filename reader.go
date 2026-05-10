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
// §9.2.6: a single leading UTF-8 Byte Order Mark is dropped if present.
//
// The implementation reads up to 3 bytes — the length of the UTF-8 BOM — and
// either drops them on a match or prepends them back via [io.MultiReader] on
// a miss. A short read (stream shorter than 3 bytes) is treated as a non-BOM
// stream; only a real I/O error is propagated.
func stripBOM(r io.Reader) (io.Reader, error) {
	var head [3]byte
	n, err := io.ReadFull(r, head[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("sse: reading BOM: %w", err)
	}
	if n == 3 && string(head[:]) == bom {
		return r, nil
	}
	return io.MultiReader(bytes.NewReader(head[:n]), r), nil
}

// newLineScanner strips the BOM from r (§9.2.6) and wraps the result in a
// [bufio.Scanner] configured to split on all three SSE line endings defined in
// §9.2.5 (CRLF, lone CR, lone LF) via [splitLine].
//
// If bufSize is positive it overrides the scanner's default 64 KiB token
// buffer; the same value is used as both the initial buffer size and the
// maximum token size passed to [bufio.Scanner.Buffer].
func newLineScanner(r io.Reader, bufSize int) (*bufio.Scanner, error) {
	stripped, err := stripBOM(r)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(stripped)
	scanner.Split(splitLine)

	if bufSize > 0 {
		scanner.Buffer(make([]byte, bufSize), bufSize)
	}

	return scanner, nil
}

// Reader parses an SSE event stream according to §9.2.6, yielding one
// [Message] per blank-line dispatch boundary.
//
// The struct fields below back the spec's dispatch state machine. lastEventID
// persists across events (the spec never resets it); dataBuf, eventType, and
// retry are cleared on each dispatch.
//
// The scanner is created lazily on the first call to [Reader.Messages] and
// reused on subsequent calls, so cross-iteration state is preserved.
type Reader struct {
	r       io.Reader
	scanner *bufio.Scanner
	bufSize int

	lastEventID string
	dataBuf     bytes.Buffer
	eventType   string
	retry       time.Duration
}

// NewReader creates a [Reader] that parses the SSE event stream from r,
// using the scanner's default 64 KiB per-line buffer. Panics if r is nil.
//
// No I/O is performed during construction; the scanner is initialised lazily
// on the first call to [Reader.Messages]. Use [NewReaderSize] when the stream
// may contain lines exceeding 64 KiB.
func NewReader(r io.Reader) *Reader {
	if r == nil {
		panic("sse: reader cannot be nil")
	}
	return &Reader{r: r}
}

// NewReaderSize creates a [Reader] with a custom per-line buffer size,
// overriding the scanner's default 64 KiB. Pass a larger value when the
// stream may contain lines exceeding 64 KiB (e.g. large JSON payloads in a
// single data field). A zero or negative value falls back to the default.
// Panics if r is nil.
func NewReaderSize(r io.Reader, bufSize int) *Reader {
	rd := NewReader(r)
	if bufSize > 0 {
		rd.bufSize = bufSize
	}
	return rd
}

// NewHTTPReader creates a [Reader] from an HTTP response, first verifying that
// the Content-Type header is "text/event-stream" as required by §9.2.5
// ("This event stream format's MIME type is text/event-stream"). Use
// [NewHTTPReaderSize] for a custom per-line buffer size.
func NewHTTPReader(resp *http.Response) (*Reader, error) {
	if err := checkSSEResponse(resp); err != nil {
		return nil, err
	}
	return NewReader(resp.Body), nil
}

// NewHTTPReaderSize is like [NewHTTPReader] but uses a custom per-line buffer
// size; see [NewReaderSize] for semantics.
func NewHTTPReaderSize(resp *http.Response, bufSize int) (*Reader, error) {
	if err := checkSSEResponse(resp); err != nil {
		return nil, err
	}
	return NewReaderSize(resp.Body, bufSize), nil
}

// checkSSEResponse validates that resp carries an SSE-compatible
// Content-Type header (§9.2.5).
func checkSSEResponse(resp *http.Response) error {
	if resp == nil {
		return errors.New("sse: http.Response cannot be nil")
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		return errors.New("sse: missing Content-Type header")
	}
	if !strings.HasPrefix(contentType, "text/event-stream") {
		return fmt.Errorf("sse: Content-Type must be 'text/event-stream', got %q", contentType)
	}
	return nil
}

// parseLine implements the per-line processing rules from §9.2.6 on a single
// non-empty line and updates the reader's field buffers in place.
//
// line is a slice owned by the scanner — invalidated by the next
// [bufio.Scanner.Scan] call. Values that must outlive this method
// (lastEventID, eventType) are converted to string at the point of storage;
// values that flow through unchanged (data, retry) avoid the per-line string
// allocation that [bufio.Scanner.Text] would impose.
func (r *Reader) parseLine(line []byte) {
	// Case 1: comment line — discard.
	if line[0] == ':' {
		return
	}

	field, value, found := bytes.Cut(line, []byte{':'})
	if found {
		// Case 2: strip a single leading space from the value (§9.2.6).
		value = bytes.TrimPrefix(value, []byte{' '})
	} else {
		// Case 3: no colon — whole line is the field name, value is empty.
		field = line
		value = nil
	}

	// switch string([]byte) is a compiler-recognised zero-allocation pattern.
	switch string(field) {
	case fieldID:
		// §9.2.6: ignore id values that contain U+0000 NULL.
		if bytes.IndexByte(value, 0) < 0 {
			r.lastEventID = string(value)
		}

	case fieldEvent:
		r.eventType = string(value)

	case fieldData:
		// §9.2.6: append value followed by U+000A LF.
		r.dataBuf.Write(value)
		r.dataBuf.WriteByte('\n')

	case fieldRetry:
		// §9.2.6: value must consist solely of ASCII digits; otherwise ignore.
		// ParseUint enforces non-empty, digits-only (rejects sign prefixes),
		// and overflow in a single call.
		ms, err := strconv.ParseUint(string(value), 10, 63)
		if err != nil {
			return
		}
		r.retry = time.Duration(ms) * time.Millisecond
	}
}

// buildMessage runs the dispatch algorithm from §9.2.6 when a blank line is
// encountered, assembling a [Message] from the accumulated field buffers.
// Returns ok=false when there is nothing to dispatch (empty data buffer);
// in that case the buffers are still cleared for the next event.
func (r *Reader) buildMessage() (Message, bool) {
	// Dispatch step 2: empty data buffer → discard.
	if r.dataBuf.Len() == 0 {
		r.eventType = ""
		r.retry = 0
		return Message{}, false
	}

	// Dispatch step 3: remove the trailing LF appended by the last "data" line.
	// dataBuf is non-empty at this point, so the last byte is the LF appended by
	// parseLine. Clone the result so it does not alias dataBuf's memory, which
	// is overwritten on the next Reset.
	buf := r.dataBuf.Bytes()
	if n := len(buf); n > 0 && buf[n-1] == '\n' {
		buf = buf[:n-1]
	}
	data := bytes.Clone(buf)

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
	return func(yield func(Message, error) bool) {
		if r.scanner == nil {
			scanner, err := newLineScanner(r.r, r.bufSize)
			if err != nil {
				yield(Message{}, err)
				return
			}
			r.scanner = scanner
		}

		for {
			if err := ctx.Err(); err != nil {
				yield(Message{}, err)
				return
			}

			if !r.scanner.Scan() {
				break
			}

			line := r.scanner.Bytes()

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

		if err := r.scanner.Err(); err != nil {
			yield(Message{}, err)
		}
	}
}
