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
	"unicode"
)

func trimTrailingCR(data []byte) []byte {
	if len(data) > 0 && data[len(data)-1] == cr[0] {
		return data[0 : len(data)-1]
	}
	return data
}

func splitLine(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	n := bytes.IndexByte(data, lf[0])
	r := bytes.IndexByte(data, cr[0])

	if n >= 0 && r >= 0 {
		if n == r+1 {
			return n + 1, trimTrailingCR(data[0:n]), nil
		}

		i := min(n, r)
		return i + 1, trimTrailingCR(data[0:i]), nil
	}

	if i := max(n, r); i >= 0 {
		return i + 1, trimTrailingCR(data[0:i]), nil
	}

	if atEOF {
		return len(data), data, nil
	}

	return 0, nil, nil
}

func stripBOM(r io.Reader) (io.Reader, error) {
	br := bufio.NewReader(r)

	char, _, err := br.ReadRune()
	if err != nil {
		return nil, err
	}

	if string(char) != bom {
		err = br.UnreadRune()
		if err != nil {
			return nil, err
		}
	}

	return br, nil
}

func newLineScanner(r io.Reader) (*bufio.Scanner, error) {
	stripped, err := stripBOM(r)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(stripped)
	scanner.Split(splitLine)

	return scanner, nil
}

type Reader struct {
	lastEventID string
	dataBuf     *bytes.Buffer
	eventType   string
	retryMs     time.Duration

	scanner *bufio.Scanner
}

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

func (r *Reader) parseLine(line string) {
	if strings.HasPrefix(line, colon) {
		return
	}

	field, value, found := strings.Cut(line, colon)
	if !found {
		field = line
		value = ""
	} else {
		value = strings.TrimPrefix(value, space)
	}

	switch field {
	case fieldID:
		if !strings.Contains(value, null) {
			r.lastEventID = value
		}

	case fieldEvent:
		r.eventType = value

	case fieldData:
		r.dataBuf.WriteString(value)
		r.dataBuf.WriteString(lf)

	case fieldRetry:
		for _, ch := range []rune(value) {
			if !unicode.IsDigit(ch) {
				return
			}
		}
		retry, err := strconv.Atoi(value)
		if err != nil {
			return
		}
		r.retryMs = time.Duration(retry) * time.Millisecond
	}
}

func (r *Reader) buildEvent() (Message, bool) {
	if r.dataBuf.Len() == 0 && r.retryMs == 0 {
		r.dataBuf.Reset()
		r.eventType = ""
		return Message{}, false
	}

	data := bytes.TrimSuffix(r.dataBuf.Bytes(), []byte(lf))

	msg := Message{
		ID:    r.lastEventID,
		Event: defaultEvent,
		Data:  data,
		Retry: r.retryMs,
	}

	if r.eventType != "" {
		msg.Event = r.eventType
	}

	r.dataBuf.Reset()
	r.eventType = ""
	r.retryMs = 0

	return msg, true
}

func (r *Reader) Messages() iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		for r.scanner.Scan() {
			line := r.scanner.Text()

			if len(line) == 0 {
				msg, ok := r.buildEvent()
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
