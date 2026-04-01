package sse

import (
	"errors"
	"io"
	"net/http"
)

var _ io.Writer = (*httpWriteFlusher)(nil)

type httpWriteFlusher struct {
	w http.ResponseWriter
	f http.Flusher
}

func (h *httpWriteFlusher) Write(p []byte) (int, error) {
	n, err := h.w.Write(p)
	if err != nil {
		return n, err
	}
	h.f.Flush()
	return n, nil
}

func setSSEHeaders(header http.Header) {
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Connection", "keep-alive")

	if len(header.Get("Cache-Control")) == 0 {
		header.Set("Cache-Control", "no-cache")
	}
}

type Writer struct {
	writer  *httpWriteFlusher
	encoder *Encoder
}

func NewWriter(w http.ResponseWriter) (*Writer, error) {
	if w == nil {
		return nil, errors.New("writer cannot be nil")
	}

	setSSEHeaders(w.Header())
	writer := &httpWriteFlusher{
		w: w,
		f: w.(http.Flusher),
	}
	encoder, err := NewEncoder(writer)
	if err != nil {
		return nil, err
	}

	return &Writer{
		writer:  writer,
		encoder: encoder,
	}, nil
}

func (w *Writer) Message(msg Message) error {
	return w.encoder.Message(msg)
}

func (w *Writer) Comment(comment string) error {
	return w.encoder.Comment(comment)
}
