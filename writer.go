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

func (w *Writer) writeID(id string, buf *bytes.Buffer) {
	if len(id) == 0 {
		return
	}

	buf.WriteString(fieldID)
	buf.WriteString(colon)
	buf.WriteString(space)
	buf.WriteString(id)
	buf.WriteString(lf)
}

func (w *Writer) writeEvent(event string, buf *bytes.Buffer) {
	if len(event) == 0 {
		return
	}

	buf.WriteString(fieldEvent)
	buf.WriteString(colon)
	buf.WriteString(space)
	buf.WriteString(event)
	buf.WriteString(lf)
}

func (w *Writer) writeData(data []byte, buf *bytes.Buffer) {
	if len(data) == 0 {
		return
	}

	data = bytes.ReplaceAll(data, []byte(cr), []byte{})

	lines := bytes.Split(data, []byte(lf))
	for _, line := range lines {
		buf.WriteString(fieldData)
		buf.WriteString(colon)
		buf.WriteString(space)
		buf.Write(line)
		buf.WriteString(lf)
	}
}

func (w *Writer) writeRetry(retry time.Duration, buf *bytes.Buffer) {
	if retry <= 0 {
		return
	}

	buf.WriteString(fieldRetry)
	buf.WriteString(colon)
	buf.WriteString(space)
	buf.WriteString(strconv.FormatInt(retry.Milliseconds(), 10))
	buf.WriteString(lf)
}

func (w *Writer) encode(msg Message) []byte {
	capacity := len(msg.ID) + len(msg.Event) + 2*len(msg.Data) + 8
	buf := bytes.NewBuffer(make([]byte, 0, capacity))

	w.writeID(msg.ID, buf)
	w.writeEvent(msg.Event, buf)
	w.writeData(msg.Data, buf)
	w.writeRetry(msg.Retry, buf)

	buf.WriteString(lf)

	return buf.Bytes()
}

func (w *Writer) Message(msg Message) error {
	_, err := w.w.Write(w.encode(msg))
	return err
}

func (w *Writer) Comment(comment string) error {
	// ": " + comment + "\n" + "\n"
	buf := bytes.NewBuffer(make([]byte, 0, len(comment)+4))

	buf.WriteString(colon)
	buf.WriteString(space)
	buf.WriteString(comment)
	buf.WriteString(lf)
	buf.WriteString(lf)

	_, err := w.w.Write(buf.Bytes())
	return err
}
