package sse

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// stripBOM reads the first rune from r and discards it if it is a UTF-8 BOM
// (U+FEFF). The returned reader always starts at the first non-BOM byte.
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

// newLineScanner wraps r in a bufio.Scanner configured to split on SSE line
// endings (CR, LF, or CRLF) after stripping a leading BOM.
func newLineScanner(r io.Reader) (*bufio.Scanner, error) {
	stripped, err := stripBOM(r)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(stripped)
	scanner.Split(splitLine)

	return scanner, nil
}

// Reader parses an SSE event stream, yielding one Message per blank-line
// dispatch boundary.
type Reader struct {
	lastEventID string
	dataBuf     *bytes.Buffer
	eventType   string
	retry       time.Duration

	scanner *bufio.Scanner
}

// NewReader creates a Reader that parses SSE events from r.
func NewReader(r io.Reader) (*Reader, error) {
	if r == nil {
		return nil, errors.New("sse: reader cannot be nil")
	}

	scanner, err := newLineScanner(r)
	if err != nil {
		return nil, err
	}

	return &Reader{
		dataBuf: bytes.NewBuffer(nil),
		scanner: scanner,
	}, nil
}

// NewHTTPReader creates a Reader from an HTTP response, validating that the
// Content-Type header is text/event-stream.
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

	return NewReader(resp.Body)
}

// parseLine processes a single non-empty SSE line and updates the reader state.
// Lines beginning with ':' are SSE comments and are silently ignored.
func (r *Reader) parseLine(line string) {
	// Lines starting with ':' are comments — discard them.
	if strings.HasPrefix(line, colon) {
		return
	}

	field, value, found := strings.Cut(line, colon)
	if !found {
		// No colon: the whole line is the field name; value is empty.
		field = line
		value = ""
	} else {
		// Strip a single leading space from the value, per the spec.
		value = strings.TrimPrefix(value, space)
	}

	switch field {
	case fieldID:
		// Ignore id values that contain a null character (U+0000).
		if !strings.Contains(value, null) {
			r.lastEventID = value
		}

	case fieldEvent:
		r.eventType = value

	case fieldData:
		r.dataBuf.WriteString(value)
		r.dataBuf.WriteString(lf)

	case fieldRetry:
		// The retry value must consist solely of ASCII digits; ignore otherwise.
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

// buildMessage assembles a Message from the accumulated data and event-type
// buffers. Returns (Message{}, false) when the data buffer is empty, since the
// spec requires discarding events with no data.
func (r *Reader) buildMessage() (Message, bool) {
	if r.dataBuf.Len() == 0 {
		r.eventType = ""
		r.retry = 0
		return Message{}, false
	}

	// The spec requires stripping the trailing LF that was appended after the
	// last data line.
	data := bytes.TrimSuffix(r.dataBuf.Bytes(), []byte(lf))

	msg := Message{
		ID:    r.lastEventID,
		Event: defaultEvent,
		Data:  data,
		Retry: r.retry,
	}

	if r.eventType != "" {
		msg.Event = r.eventType
	}

	r.dataBuf.Reset()
	r.eventType = ""
	r.retry = 0

	return msg, true
}

// Messages returns an iterator over all SSE messages in the stream.
// Scanner errors are surfaced as the error value of the final iteration;
// the iterator stops immediately after yielding the error.
func (r *Reader) Messages() iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		for r.scanner.Scan() {
			line := r.scanner.Text()

			if len(line) == 0 {
				// Blank line: attempt to dispatch the current event.
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
