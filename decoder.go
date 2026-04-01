package sse

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"iter"
	"strconv"
	"strings"
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

func stripBOM(reader io.Reader) (io.Reader, error) {
	br := bufio.NewReader(reader)

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

func newLineScanner(reader io.Reader) (*bufio.Scanner, error) {
	stripped, err := stripBOM(reader)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(stripped)
	scanner.Split(splitLine)

	return scanner, nil
}

type Decoder struct {
	lastEventID string
	dataBuf     *bytes.Buffer
	eventType   string
	retryMs     int

	scanner *bufio.Scanner
}

func NewDecoder(reader io.Reader) (*Decoder, error) {
	if reader == nil {
		return nil, errors.New("sse: decoder reader cannot be nil")
	}

	scanner, err := newLineScanner(reader)
	if err != nil {
		return nil, err
	}

	return &Decoder{
		lastEventID: "",
		dataBuf:     bytes.NewBuffer(nil),
		eventType:   "",
		retryMs:     0,
		scanner:     scanner,
	}, nil
}

func (d *Decoder) parseLine(line string) {
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
			d.lastEventID = value
		}

	case fieldEvent:
		d.eventType = value

	case fieldData:
		d.dataBuf.WriteString(value)
		d.dataBuf.WriteString(lf)

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
		d.retryMs = retry
	}
}

func (d *Decoder) buildEvent() (Message, bool) {
	if d.dataBuf.Len() == 0 && d.retryMs == 0 {
		d.dataBuf.Reset()
		d.eventType = ""
		return Message{}, false
	}

	data := bytes.TrimSuffix(d.dataBuf.Bytes(), []byte(lf))

	msg := Message{
		ID:    d.lastEventID,
		Event: defaultEvent,
		Data:  data,
		Retry: d.retryMs,
	}

	if d.eventType != "" {
		msg.Event = d.eventType
	}

	d.dataBuf.Reset()
	d.eventType = ""
	d.retryMs = 0

	return msg, true
}

func (d *Decoder) Messages() iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		for d.scanner.Scan() {
			if err := d.scanner.Err(); err != nil {
				yield(Message{}, err)
				return
			}

			line := d.scanner.Text()

			if len(line) == 0 {
				msg, ok := d.buildEvent()
				if ok {
					if !yield(msg, nil) {
						return
					}
				}
				continue
			}

			d.parseLine(line)
		}
	}
}
