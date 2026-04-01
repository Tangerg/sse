package sse

import (
	"errors"
	"fmt"
	"iter"
	"net/http"
	"strings"
)

type Reader struct {
	httpResponse *http.Response
	decoder      *Decoder
}

func NewReader(httpResponse *http.Response) (*Reader, error) {
	if httpResponse == nil {
		return nil, errors.New("http response is nil")
	}
	contentType := httpResponse.Header.Get("Content-Type")
	if contentType == "" {
		return nil, errors.New("sse: missing Content-Type header")
	}

	if !strings.HasPrefix(contentType, "text/event-stream") {
		return nil, fmt.Errorf("sse: expected Content-Type 'text/event-stream', got %s", contentType)
	}

	decoder, err := NewDecoder(httpResponse.Body)
	if err != nil {
		return nil, err
	}

	return &Reader{
		httpResponse: httpResponse,
		decoder:      decoder,
	}, nil
}

func (r *Reader) Messages() iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		defer r.httpResponse.Body.Close()

		for message, err := range r.decoder.Messages() {
			if !yield(message, err) {
				return
			}
		}
	}
}
