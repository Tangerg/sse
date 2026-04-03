package sse

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"time"
)

type Encoder struct {
	w io.Writer
}

func NewEncoder(w io.Writer) (*Encoder, error) {
	if w == nil {
		return nil, errors.New("sse: encoder writer cannot be nil")
	}
	return &Encoder{w: w}, nil
}

func (e *Encoder) writeID(id string, buf *bytes.Buffer) {
	if len(id) == 0 {
		return
	}

	buf.WriteString(fieldID)
	buf.WriteString(colon)
	buf.WriteString(space)
	buf.WriteString(id)
	buf.WriteString(lf)
}

func (e *Encoder) writeEvent(event string, buf *bytes.Buffer) {
	if len(event) == 0 {
		return
	}

	buf.WriteString(fieldEvent)
	buf.WriteString(colon)
	buf.WriteString(space)
	buf.WriteString(event)
	buf.WriteString(lf)
}

func (e *Encoder) writeData(data []byte, buf *bytes.Buffer) {
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

func (e *Encoder) writeRetry(retry time.Duration, buf *bytes.Buffer) {
	if retry <= 0 {
		return
	}

	buf.WriteString(fieldRetry)
	buf.WriteString(colon)
	buf.WriteString(space)
	buf.WriteString(strconv.FormatInt(retry.Milliseconds(), 10))
	buf.WriteString(lf)
}

func (e *Encoder) encode(msg Message) ([]byte, error) {
	capacity := len(msg.ID) + len(msg.Event) + 2*len(msg.Data) + 8
	buf := bytes.NewBuffer(make([]byte, 0, capacity))

	e.writeID(msg.ID, buf)
	e.writeEvent(msg.Event, buf)
	e.writeData(msg.Data, buf)
	e.writeRetry(msg.Retry, buf)

	buf.WriteString(lf)

	return buf.Bytes(), nil
}

func (e *Encoder) Message(msg Message) error {
	encoded, err := e.encode(msg)
	if err != nil {
		return err
	}

	_, err = e.w.Write(encoded)
	return err
}

func (e *Encoder) Comment(comment string) error {
	// ": " + comment + "\n" + "\n"
	buf := bytes.NewBuffer(make([]byte, 0, len(comment)+4))

	buf.WriteString(colon)
	buf.WriteString(space)
	buf.WriteString(comment)
	buf.WriteString(lf)
	buf.WriteString(lf)

	_, err := e.w.Write(buf.Bytes())
	return err
}
