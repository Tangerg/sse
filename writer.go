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

// flushWriter wraps an http.ResponseWriter and flushes after every write,
// ensuring each SSE event is sent to the client immediately.
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

// setSSEHeaders writes the required SSE response headers.
// Cache-Control defaults to "no-cache" if not already set by the caller.
func setSSEHeaders(header http.Header) {
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Connection", "keep-alive")

	if header.Get("Cache-Control") == "" {
		header.Set("Cache-Control", "no-cache")
	}
}

// Writer serialises SSE messages to an io.Writer.
type Writer struct {
	w io.Writer
}

// NewWriter creates a Writer that encodes SSE events to w.
func NewWriter(w io.Writer) (*Writer, error) {
	if w == nil {
		return nil, errors.New("sse: writer cannot be nil")
	}
	return &Writer{w: w}, nil
}

// NewHTTPWriter creates a Writer for an HTTP response. It sets the required SSE
// headers and wraps rw in a flushWriter so each event is flushed immediately.
// rw must implement http.Flusher; most standard library handlers do.
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

// fieldBuf is an in-memory buffer for building a single SSE event frame.
type fieldBuf struct {
	*bytes.Buffer
}

func newFieldBuf(capacity int) *fieldBuf {
	return &fieldBuf{bytes.NewBuffer(make([]byte, 0, capacity))}
}

// newlineStripper removes CR and LF from field values before writing.
// Per the SSE grammar, any-char excludes U+000A (LF) and U+000D (CR).
var newlineStripper = strings.NewReplacer("\r\n", "", "\r", "", "\n", "")

// write appends one SSE line in the form "field: value\n".
// If field is empty, the line becomes a comment (": value\n").
func (b *fieldBuf) write(field, value string) {
	if field != "" {
		b.WriteString(field)
	}
	b.WriteString(colon)
	b.WriteString(space)
	b.WriteString(newlineStripper.Replace(value))
	b.WriteString(lf)
}

func (b *fieldBuf) writeID(id string) {
	if len(id) == 0 {
		return
	}
	b.write(fieldID, id)
}

func (b *fieldBuf) writeEvent(event string) {
	if len(event) == 0 {
		return
	}
	b.write(fieldEvent, event)
}

// writeData writes one "data: …\n" line per logical line in data.
// If data ends with a line terminator, an extra empty data line is written so
// the reader reconstructs the correct trailing newline.
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

func (b *fieldBuf) writeRetry(retry time.Duration) {
	if retry <= 0 {
		return
	}
	b.write(fieldRetry, strconv.FormatInt(retry.Milliseconds(), 10))
}

func (b *fieldBuf) writeComment(comment string) {
	if len(comment) == 0 {
		return
	}
	b.write("", comment)
}

// Message encodes msg as an SSE event frame and writes it to the underlying
// writer. Fields with zero values are omitted per the SSE spec.
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

// Comment encodes comment as an SSE comment line (": comment\n\n") and writes
// it to the underlying writer. Empty comments are silently ignored.
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
