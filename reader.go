package sse

import (
	"errors"
	"fmt"
	"iter"
	"net/http"
	"strings"
)

type Reader struct {
	resp    *http.Response
	decoder *Decoder
}

func NewReader(resp *http.Response) (*Reader, error) {
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

	decoder, err := NewDecoder(resp.Body)
	if err != nil {
		return nil, err
	}

	return &Reader{
		resp:    resp,
		decoder: decoder,
	}, nil
}

func (r *Reader) Messages() iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		defer r.resp.Body.Close()

		for msg, err := range r.decoder.Messages() {
			if !yield(msg, err) {
				return
			}
		}
	}
}
