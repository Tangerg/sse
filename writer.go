package sse

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

var _ io.Writer = (*httpWriteFlusher)(nil)

type httpWriteFlusher struct {
	rw      http.ResponseWriter
	flusher http.Flusher
}

func (h *httpWriteFlusher) Write(p []byte) (int, error) {
	n, err := h.rw.Write(p)
	if err != nil {
		return n, err
	}
	h.flusher.Flush()
	return n, nil
}

func applySSEHeaders(header http.Header) {
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Connection", "keep-alive")

	if header.Get("Cache-Control") == "" {
		header.Set("Cache-Control", "no-cache")
	}
}

type Writer struct {
	w io.Writer
}

func NewWriter(w io.Writer) (*Writer, error) {
	if w == nil {
		return nil, errors.New("sse: writer cannot be nil")
	}
	return &Writer{w: w}, nil
}

func NewHTTPWriter(rw http.ResponseWriter) (*Writer, error) {
	if rw == nil {
		return nil, errors.New("sse: http.ResponseWriter cannot be nil")
	}

	flusher, ok := rw.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("sse: %T does not implement http.Flusher", rw)
	}

	applySSEHeaders(rw.Header())

	return &Writer{
		w: &httpWriteFlusher{
			rw:      rw,
			flusher: flusher,
		},
	}, nil
}

type eventBuf struct {
	*bytes.Buffer
}

func newEventBuf(capacity int) *eventBuf {
	return &eventBuf{bytes.NewBuffer(make([]byte, 0, capacity))}
}

// write writes one SSE line in the form "field: value\n".
// If field is empty, it writes a comment line ": value\n".
func (b *eventBuf) write(field, value string) {
	if field != "" {
		b.WriteString(field)
	}
	b.WriteString(colon)
	b.WriteString(space)
	b.WriteString(value)
	b.WriteString(lf)
}

func (b *eventBuf) writeID(id string) {
	if len(id) == 0 {
		return
	}
	b.write(fieldID, id)
}

func (b *eventBuf) writeEvent(event string) {
	if len(event) == 0 {
		return
	}
	b.write(fieldEvent, event)
}

func (b *eventBuf) writeData(data []byte) {
	if len(data) == 0 {
		return
	}

	data = bytes.ReplaceAll(data, []byte(cr), []byte{})

	for _, line := range bytes.Split(data, []byte(lf)) {
		b.write(fieldData, string(line))
	}
}

func (b *eventBuf) writeRetry(retry time.Duration) {
	if retry <= 0 {
		return
	}
	b.write(fieldRetry, strconv.FormatInt(retry.Milliseconds(), 10))
}

func (w *Writer) encode(msg Message) []byte {
	buf := newEventBuf(len(msg.ID) + len(msg.Event) + 2*len(msg.Data) + 8)

	buf.writeID(msg.ID)
	buf.writeEvent(msg.Event)
	buf.writeData(msg.Data)
	buf.writeRetry(msg.Retry)
	buf.WriteString(lf)

	return buf.Bytes()
}

func (w *Writer) Message(msg Message) error {
	_, err := w.w.Write(w.encode(msg))
	return err
}

func (w *Writer) Comment(comment string) error {
	buf := newEventBuf(len(comment) + 4)
	buf.write("", comment)
	buf.WriteString(lf)

	_, err := w.w.Write(buf.Bytes())
	return err
}
