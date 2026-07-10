package sse

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	"strconv"
	"time"
)

// defaultMaxLineBytes matches the default maximum token size of bufio.Scanner.
const defaultMaxLineBytes = 64 * 1024

// maxRetryMS is the largest retry value, in milliseconds, that can be converted
// to a time.Duration without overflowing int64 nanoseconds. Larger values are
// clamped to the maximum duration rather than wrapping to a negative one.
const maxRetryMS = uint64(math.MaxInt64 / int64(time.Millisecond))

// ErrEventTooLarge is reported by Messages when a single event's buffered data
// exceeds Reader.MaxEventBytes.
var ErrEventTooLarge = errors.New("sse: event too large")

// Reader parses an SSE event stream (§9.2.6), yielding one Message per
// blank-line dispatch boundary.
//
// A Reader is not safe for concurrent use.
type Reader struct {
	// MaxLineBytes caps the size in bytes of a single line (one field). A value
	// of zero or less uses the 64 KiB default. It must be set before the first
	// call to Messages; later changes have no effect. Raise it when a single
	// data field may exceed 64 KiB (e.g. a large JSON payload).
	MaxLineBytes int

	// MaxEventBytes caps the total data buffered for a single event across all
	// its data lines. A value of zero or less means no limit. When the limit is
	// exceeded Messages yields ErrEventTooLarge. Set it when consuming untrusted
	// streams, where many small data lines could otherwise grow without bound.
	MaxEventBytes int

	r       io.Reader
	scanner *bufio.Scanner
	err     error // terminal error: once set, parsing never resumes

	// Dispatch state (§9.2.6). idBuffer is the last-event-ID buffer, updated as
	// soon as an id field is parsed. lastEventID is the "last event ID string",
	// copied from idBuffer at each dispatch (step 1) and used for reconnection;
	// it therefore ignores an id in a trailing event that never dispatched.
	// retry is stream-level and persists across events; dataBuf and eventType
	// are cleared on each dispatch.
	idBuffer    string
	lastEventID string
	dataBuf     bytes.Buffer
	eventType   string
	retry       time.Duration
	retrySet    bool
}

// NewReader returns a Reader that parses the SSE stream from r. It panics if r
// is nil. No I/O happens during construction; the stream is read lazily on the
// first call to Messages.
func NewReader(r io.Reader) *Reader {
	if r == nil {
		panic("sse: reader cannot be nil")
	}
	return &Reader{r: r}
}

// stripBOM drops a single leading UTF-8 BOM if present (§9.2.6, first step of
// the UTF-8 decode algorithm). It reads up to three bytes and either consumes
// them on a match or prepends them back via io.MultiReader on a miss. A stream
// shorter than three bytes is treated as having no BOM; only a real I/O error
// is propagated.
func stripBOM(r io.Reader) (io.Reader, error) {
	var head [3]byte
	n, err := io.ReadFull(r, head[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, fmt.Errorf("sse: reading BOM: %w", err)
	}
	if n == 3 && string(head[:]) == bom {
		return r, nil
	}
	return io.MultiReader(bytes.NewReader(head[:n]), r), nil
}

// Retry reports the stream's current reconnection time and whether the stream
// has set one. Because a retry field may arrive on its own (without an event)
// and then the connection ends, callers should read this after Messages returns
// to obtain the final reconnection hint. Retry is stream-level state and
// persists across events (§9.2.6).
func (r *Reader) Retry() (time.Duration, bool) {
	return r.retry, r.retrySet
}

// LastEventID reports the last event ID string (§9.2.6): the id in effect at the
// most recent dispatch, suitable for a Last-Event-ID header on reconnection. An
// id in a trailing event that never dispatched does not affect it.
func (r *Reader) LastEventID() string {
	return r.lastEventID
}

// Messages returns an iterator over every event in the stream.
//
// The iterator scans line by line: non-empty lines update the field buffers and
// blank lines dispatch the accumulated event (§9.2.6). A blank line whose data
// buffer is empty dispatches nothing and is skipped.
//
// The pair yielded on failure carries a zero Message and a non-nil error (an
// I/O error, a line exceeding MaxLineBytes, or ErrEventTooLarge); the iterator
// then stops. A clean end of stream (EOF) yields nothing — the loop simply
// ends. Per §9.2.6, data left buffered at EOF without a trailing blank line is
// discarded.
//
// There is no context parameter: to interrupt a read that is blocked on a
// stalled connection, close the underlying reader (e.g. resp.Body.Close());
// to stop consuming, break out of the range loop. The scanner is created lazily
// on the first call and reused across calls, so state is preserved between them.
func (r *Reader) Messages() iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		// A prior error is terminal: never resume parsing after one.
		if r.err != nil {
			yield(Message{}, r.err)
			return
		}
		if r.scanner == nil {
			stripped, err := stripBOM(r.r)
			if err != nil {
				r.err = err
				yield(Message{}, err)
				return
			}
			max := r.MaxLineBytes
			if max <= 0 {
				max = defaultMaxLineBytes
			}
			sc := bufio.NewScanner(stripped)
			sc.Split(splitLine)
			sc.Buffer(make([]byte, 0, min(4096, max)), max)
			r.scanner = sc
		}

		for r.scanner.Scan() {
			line := r.scanner.Bytes()
			if len(line) == 0 {
				if msg, ok := r.dispatch(); ok {
					if !yield(msg, nil) {
						return
					}
				}
				continue
			}
			if err := r.parseLine(line); err != nil {
				r.err = err
				yield(Message{}, err)
				return
			}
		}

		if err := r.scanner.Err(); err != nil {
			r.err = err
			yield(Message{}, err)
		}
	}
}

// parseLine applies the §9.2.6 per-line rules to one non-empty line, updating
// the field buffers in place. It returns ErrEventTooLarge when appending data
// would exceed MaxEventBytes.
//
// line is owned by the scanner and is invalidated by the next Scan. Values that
// must outlive this call (idBuffer, eventType) are copied to string at the point
// of storage; data flows straight into dataBuf.
func (r *Reader) parseLine(line []byte) error {
	// Comment line — ignored.
	if line[0] == ':' {
		return nil
	}

	field, value, found := bytes.Cut(line, []byte{':'})
	if found {
		// Strip a single leading space from the value (§9.2.6).
		value = bytes.TrimPrefix(value, []byte{' '})
	} else {
		// No colon — the whole line is the field name and the value is empty.
		field = line
		value = nil
	}

	// switch string([]byte) is a compiler-recognised zero-allocation pattern.
	switch string(field) {
	case fieldID:
		// §9.2.6: ignore id values that contain U+0000 NULL.
		if bytes.IndexByte(value, 0) < 0 {
			r.idBuffer = string(value)
		}
	case fieldEvent:
		r.eventType = string(value)
	case fieldData:
		// §9.2.6: append the value followed by U+000A LF. Check the limit before
		// writing so oversized data never enters the buffer; the subtraction
		// avoids the overflow that len+len could produce.
		if max := r.MaxEventBytes; max > 0 {
			needed := len(value) + 1 // value + LF
			if needed > max || r.dataBuf.Len() > max-needed {
				return ErrEventTooLarge
			}
		}
		r.dataBuf.Write(value)
		r.dataBuf.WriteByte('\n')
	case fieldRetry:
		// §9.2.6: the value must consist solely of ASCII digits; otherwise the
		// field is ignored entirely. Validate that first — ParseUint can report
		// ErrRange on a numeric overflow before it reaches a trailing non-digit,
		// which would otherwise be misread as a valid (clamped) value. After the
		// check, the only possible ParseUint error is overflow of an all-digit
		// value, which is a valid integer per the spec and is clamped.
		if !isASCIIDigits(value) {
			return nil
		}
		ms, err := strconv.ParseUint(string(value), 10, 64)
		switch {
		case err != nil, ms > maxRetryMS: // overflow of an all-digit value → clamp
			r.retry = time.Duration(math.MaxInt64)
		default:
			r.retry = time.Duration(ms) * time.Millisecond
		}
		r.retrySet = true
	}
	return nil
}

// isASCIIDigits reports whether b is non-empty and consists solely of the ASCII
// digits 0-9, as required for a retry value by §9.2.6.
func isASCIIDigits(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// dispatch runs the §9.2.6 dispatch algorithm for a blank line. Step 1 copies
// the id buffer into the last event ID string on every dispatch attempt, before
// the empty-data check, so a standalone "id:" frame still updates reconnection
// state. It reports ok=false when the data buffer is empty (nothing to
// dispatch), still clearing the event-type buffer. The retry value is
// stream-level and is never reset here.
func (r *Reader) dispatch() (Message, bool) {
	// Step 1.
	r.lastEventID = r.idBuffer

	// Step 2: empty data buffer → nothing dispatched.
	if r.dataBuf.Len() == 0 {
		r.eventType = ""
		return Message{}, false
	}

	// Step 3: remove the trailing LF appended after the last data line.
	buf := r.dataBuf.Bytes()
	if n := len(buf); n > 0 && buf[n-1] == '\n' {
		buf = buf[:n-1]
	}
	// Clone so the data does not alias dataBuf's storage, which Reset reuses.
	data := bytes.Clone(buf)

	event := r.eventType
	if event == "" {
		event = defaultEvent
	}

	r.dataBuf.Reset()
	r.eventType = ""

	return Message{
		ID:    r.lastEventID,
		Event: event,
		Data:  data,
		Retry: r.retry,
	}, true
}
