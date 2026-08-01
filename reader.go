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
	"unicode/utf8"
)

// defaultMaxLineBytes matches the default maximum token size of bufio.Scanner.
const defaultMaxLineBytes = 64 * 1024

// maxRetryMS is the largest retry value, in milliseconds, that can be converted
// to a time.Duration without overflowing int64 nanoseconds. Larger values are
// clamped to the maximum duration rather than wrapping to a negative one.
const maxRetryMS = uint64(math.MaxInt64 / int64(time.Millisecond))

// ErrEventTooLarge is reported by [Reader.Read] when a single event's buffered
// data exceeds [Reader.MaxEventBytes]; ErrLineTooLong when a single line exceeds
// [Reader.MaxLineBytes]. [Reader.Messages] yields the same errors. Both are
// matchable with [errors.Is].
var (
	ErrEventTooLarge = errors.New("sse: event too large")
	ErrLineTooLong   = errors.New("sse: line exceeds MaxLineBytes")
)

// Reader parses an SSE event stream (§9.2.6), yielding one Message per
// blank-line dispatch boundary.
//
// A Reader is not safe for concurrent use.
type Reader struct {
	// MaxLineBytes caps the size in bytes of a single line (one field), excluding
	// its CR/LF terminator. A value of zero or less uses the 64 KiB default. It
	// must be set before the first call to Read or Messages; later changes have no
	// effect. Raise it when a single data field may exceed 64 KiB (e.g. a large
	// JSON payload).
	MaxLineBytes int

	// MaxEventBytes caps the total data buffered for a single event across all
	// its data lines. A value of zero or less means no limit. When the limit is
	// exceeded Read returns ErrEventTooLarge and Messages yields it. Set the limit
	// when consuming untrusted streams, where many small data lines could
	// otherwise grow without bound.
	MaxEventBytes int

	r       io.Reader
	scanner *bufio.Scanner
	err     error // terminal result, including io.EOF
	maxLine int   // effective MaxLineBytes captured when scanner is created

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
// first call to Read or Messages.
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

// Read reads and returns the next event in the stream.
//
// Read skips comments and control-only frames. It returns [io.EOF] after a
// clean end of stream; per §9.2.6, any event left incomplete at EOF is
// discarded. On failure it returns a zero [Message] and the underlying I/O
// error, [ErrLineTooLong], or [ErrEventTooLarge]. EOF and errors are terminal:
// subsequent calls return the same result.
//
// The scanner is created lazily on the first call. Read and [Reader.Messages]
// share the same stream position and may be used interchangeably, but a Reader
// must not be used concurrently.
func (r *Reader) Read() (Message, error) {
	if r.err != nil {
		return Message{}, r.err
	}
	if r.scanner == nil {
		if err := r.initScanner(); err != nil {
			r.err = err
			return Message{}, err
		}
	}

	for r.scanner.Scan() {
		line := r.scanner.Bytes()
		if len(line) > r.maxLine {
			r.err = ErrLineTooLong
			return Message{}, r.err
		}
		line = decodeUTF8(line)
		if len(line) == 0 {
			if msg, ok := r.dispatch(); ok {
				return msg, nil
			}
			continue
		}
		if err := r.parseLine(line); err != nil {
			r.err = err
			return Message{}, err
		}
	}

	r.err = r.scanner.Err()
	if errors.Is(r.err, bufio.ErrTooLong) {
		// Normalise the scanner's buffer error to the package sentinel so both
		// over-limit paths have the same matchable result.
		r.err = ErrLineTooLong
	}
	if r.err == nil {
		r.err = io.EOF
	}
	return Message{}, r.err
}

// Messages returns an iterator over every event in the stream.
//
// The iterator scans line by line: non-empty lines update the field buffers and
// blank lines dispatch the accumulated event (§9.2.6). A blank line whose data
// buffer is empty dispatches nothing and is skipped.
//
// The pair yielded on failure carries a zero Message and a non-nil error from
// [Reader.Read]; the iterator then stops. A clean end of stream yields nothing.
//
// There is no context parameter: to interrupt a read that is blocked on a
// stalled connection, close the underlying reader (e.g. resp.Body.Close());
// to stop consuming, break out of the range loop. The scanner is created lazily
// on the first call and reused across calls, so state is preserved between them.
func (r *Reader) Messages() iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		for {
			msg, err := r.Read()
			if err == io.EOF {
				return
			}
			if err != nil {
				yield(Message{}, err)
				return
			}
			if !yield(msg, nil) {
				return
			}
		}
	}
}

// initScanner lazily builds the line scanner: it strips a leading BOM, splits on
// SSE line endings, and sizes the buffer to hold one MaxLineBytes token plus the
// framing overhead the scanner needs for EOF detection and CRLF terminators
// (kept out of the advertised limit, which applies only to token bytes).
func (r *Reader) initScanner() error {
	stripped, err := stripBOM(r.r)
	if err != nil {
		return err
	}
	max := r.MaxLineBytes
	if max <= 0 {
		max = defaultMaxLineBytes
	}
	scannerMax := max
	if max <= math.MaxInt-2 {
		scannerMax += 2
	}
	sc := bufio.NewScanner(stripped)
	sc.Split(splitLine)
	sc.Buffer(make([]byte, 0, min(4096, scannerMax)), scannerMax)
	r.scanner = sc
	r.maxLine = max
	return nil
}

// decodeUTF8 applies the Encoding Standard's UTF-8 decoder in replacement
// mode to one complete SSE line. Valid input is returned without allocation;
// malformed subsequences are replaced with U+FFFD.
//
// Decoding line by line is equivalent to decoding the stream first because CR
// and LF are ASCII bytes: neither can occur in a valid multi-byte sequence, and
// the decoder reprocesses either byte as a line ending after an error.
//
// This is hand-rolled, not utf8.DecodeRune-in-a-loop nor bytes.ToValidUTF8,
// because only this matches the standard's "maximal subpart" replacement, which
// the Web Platform Tests pin by exact U+FFFD count. The stdlib options each
// diverge on a different input: for "E0 A0 20" a DecodeRune loop emits two
// U+FFFD (want one), and for "E0 80 80" bytes.ToValidUTF8 emits one (want
// three). Do not replace this with either.
func decodeUTF8(p []byte) []byte {
	if utf8.Valid(p) {
		return p
	}

	out := make([]byte, 0, len(p))
	var seq [utf8.UTFMax]byte
	var seqLen, bytesSeen, bytesNeeded int
	lower, upper := byte(0x80), byte(0xbf)

	reset := func() {
		seqLen, bytesSeen, bytesNeeded = 0, 0, 0
		lower, upper = 0x80, 0xbf
	}
	replacement := func() {
		out = append(out, "\uFFFD"...)
		reset()
	}

	for i := 0; i < len(p); {
		b := p[i]
		if bytesNeeded == 0 {
			switch {
			case b <= 0x7f:
				out = append(out, b)
				i++
			case b >= 0xc2 && b <= 0xdf:
				seq[0], seqLen, bytesNeeded = b, 1, 1
				i++
			case b >= 0xe0 && b <= 0xef:
				seq[0], seqLen, bytesNeeded = b, 1, 2
				if b == 0xe0 {
					lower = 0xa0
				}
				if b == 0xed {
					upper = 0x9f
				}
				i++
			case b >= 0xf0 && b <= 0xf4:
				seq[0], seqLen, bytesNeeded = b, 1, 3
				if b == 0xf0 {
					lower = 0x90
				}
				if b == 0xf4 {
					upper = 0x8f
				}
				i++
			default:
				replacement()
				i++
			}
			continue
		}

		if b < lower || b > upper {
			// Do not consume b: the decoder restores the offending byte to the
			// input queue and processes it again from the initial state.
			replacement()
			continue
		}
		lower, upper = 0x80, 0xbf
		seq[seqLen] = b
		seqLen++
		bytesSeen++
		i++
		if bytesSeen == bytesNeeded {
			out = append(out, seq[:seqLen]...)
			reset()
		}
	}

	if bytesNeeded != 0 {
		replacement()
	}
	return out
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
			// Account for the appended LF without computing len(value)+1, which
			// could overflow for a theoretical maximum-sized slice.
			if len(value) >= max || r.dataBuf.Len() > max-len(value)-1 {
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
	}, true
}
