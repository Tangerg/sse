package sse

import (
	"errors"
	"fmt"
	"io"
	"net/http"
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
	wf      *httpWriteFlusher
	encoder *Encoder
}

func NewWriter(w http.ResponseWriter) (*Writer, error) {
	if w == nil {
		return nil, errors.New("sse: http.ResponseWriter cannot be nil")
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("sse: %T does not implement http.Flusher", w)
	}

	applySSEHeaders(w.Header())

	wf := &httpWriteFlusher{
		rw:      w,
		flusher: flusher,
	}

	encoder, err := NewEncoder(wf)
	if err != nil {
		return nil, err
	}

	return &Writer{
		wf:      wf,
		encoder: encoder,
	}, nil
}

func (w *Writer) Message(msg Message) error {
	return w.encoder.Message(msg)
}

func (w *Writer) Comment(comment string) error {
	return w.encoder.Comment(comment)
}
