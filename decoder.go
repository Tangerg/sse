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

func dropCR(data []byte) []byte {
	if len(data) > 0 && data[len(data)-1] == cr[0] {
		return data[0 : len(data)-1]
	}
	return data
}

func scanLinesSplit(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	n := bytes.IndexByte(data, lf[0])
	r := bytes.IndexByte(data, cr[0])

	if n >= 0 && r >= 0 {
		if n == r+1 {
			return n + 1, dropCR(data[0:n]), nil
		}

		i := min(n, r)
		return i + 1, dropCR(data[0:i]), nil
	}
	if i := max(n, r); i >= 0 {
		return i + 1, dropCR(data[0:i]), nil
	}

	if atEOF {
		return len(data), data, nil
	}

	return 0, nil, nil
}

func skipLeadingUTF8BOM(reader io.Reader) (io.Reader, error) {
	bufioReader := bufio.NewReader(reader)
	char, _, err := bufioReader.ReadRune()
	if err != nil {
		return nil, err
	}
	if string(char) != bom {
		err = bufioReader.UnreadRune()
		if err != nil {
			return nil, err
		}
	}
	return bufioReader, nil
}

func getLineScanner(reader io.Reader) (*bufio.Scanner, error) {
	utf8BOM, err := skipLeadingUTF8BOM(reader)
	if err != nil {
		return nil, err
	}
	lineScanner := bufio.NewScanner(utf8BOM)
	lineScanner.Split(scanLinesSplit)

	return lineScanner, nil
}

type Decoder struct {
	lastIDBuffer string
	dataBuffer   *bytes.Buffer
	eventBuffer  string
	retryBuffer  int

	lineScanner *bufio.Scanner
}

func NewDecoder(reader io.Reader) (*Decoder, error) {
	if reader == nil {
		return nil, errors.New("nil reader")
	}

	lineScanner, err := getLineScanner(reader)
	if err != nil {
		return nil, err
	}
	return &Decoder{
		lastIDBuffer: "",
		dataBuffer:   bytes.NewBuffer(nil),
		eventBuffer:  "",
		retryBuffer:  0,
		lineScanner:  lineScanner,
	}, nil
}

func (d *Decoder) processLine(line string) {
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
			d.lastIDBuffer = value
		}

	case fieldEvent:
		d.eventBuffer = value

	case fieldData:
		d.dataBuffer.WriteString(value)
		d.dataBuffer.WriteString(lf)

	case fieldRetry:
		for _, char := range []rune(value) {
			if !unicode.IsDigit(char) {
				return
			}
		}
		retry, err := strconv.Atoi(value)
		if err != nil {
			return
		}
		d.retryBuffer = retry
	}
}

func (d *Decoder) dispatch() (Message, bool) {
	if d.dataBuffer.Len() == 0 &&
		d.retryBuffer == 0 {
		d.dataBuffer.Reset()
		d.eventBuffer = ""
		return Message{}, false
	}

	data := bytes.TrimSuffix(d.dataBuffer.Bytes(), []byte(lf))

	msg := Message{
		ID:    d.lastIDBuffer,
		Event: eventMessage,
		Data:  data,
		Retry: d.retryBuffer,
	}

	if d.eventBuffer != "" {
		msg.Event = d.eventBuffer
	}

	d.dataBuffer.Reset()
	d.eventBuffer = ""
	d.retryBuffer = 0

	return msg, true
}

func (d *Decoder) Messages() iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		for d.lineScanner.Scan() {
			err := d.lineScanner.Err()
			if err != nil {
				yield(Message{}, err)
				return
			}
			currentLine := d.lineScanner.Text()
			if len(currentLine) == 0 {
				message, ok := d.dispatch()
				if !ok {
					continue
				}
				if !yield(message, nil) {
					return
				}
			}
			d.processLine(currentLine)
		}
	}
}
