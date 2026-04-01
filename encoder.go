package sse

import (
	"bytes"
	"errors"
	"io"
	"strconv"
)

type Encoder struct {
	w io.Writer
}

func NewEncoder(w io.Writer) (*Encoder, error) {
	if w == nil {
		return nil, errors.New("w cannot be nil")
	}
	return &Encoder{w: w}, nil
}

func (e *Encoder) writeID(id string, buffer *bytes.Buffer) {
	if len(id) == 0 {
		return
	}

	buffer.WriteString(fieldID)
	buffer.WriteString(colon)
	buffer.WriteString(space)
	buffer.WriteString(id)
	buffer.WriteString(lf)
}

func (e *Encoder) writeEvent(event string, buffer *bytes.Buffer) {
	if len(event) == 0 {
		return
	}

	buffer.WriteString(fieldEvent)
	buffer.WriteString(colon)
	buffer.WriteString(space)
	buffer.WriteString(event)
	buffer.WriteString(lf)
}

func (e *Encoder) writeData(data []byte, buffer *bytes.Buffer) {
	if len(data) == 0 {
		return
	}

	data = bytes.ReplaceAll(data, []byte(cr), []byte{})

	lines := bytes.Split(data, []byte(lf))
	for _, line := range lines {
		buffer.WriteString(fieldData)
		buffer.WriteString(colon)
		buffer.WriteString(space)
		buffer.Write(line)
		buffer.WriteString(lf)
	}
}

func (e *Encoder) writeRetry(retry int, buffer *bytes.Buffer) {
	if retry <= 0 {
		return
	}
	buffer.WriteString(fieldRetry)
	buffer.WriteString(colon)
	buffer.WriteString(space)
	buffer.WriteString(strconv.Itoa(retry))
	buffer.WriteString(lf)
}

func (e *Encoder) encodeMessage(msg Message) ([]byte, error) {
	estimatedCapacity := len(msg.ID) + len(msg.Event) + 2*len(msg.Data) + 8
	buffer := bytes.NewBuffer(make([]byte, 0, estimatedCapacity))

	e.writeID(msg.ID, buffer)
	e.writeEvent(msg.Event, buffer)
	e.writeData(msg.Data, buffer)
	e.writeRetry(msg.Retry, buffer)

	buffer.WriteString(lf)

	return buffer.Bytes(), nil
}

func (e *Encoder) Message(msg Message) error {
	chars, err := e.encodeMessage(msg)
	if err != nil {
		return err
	}

	_, err = e.w.Write(chars)
	return err
}

func (e *Encoder) Comment(comment string) error {
	estimatedCapacity := len(comment)
	buffer := bytes.NewBuffer(make([]byte, 0, estimatedCapacity))
	buffer.WriteString(colon)
	buffer.WriteString(space)
	buffer.WriteString(comment)
	buffer.WriteString(lf)
	buffer.WriteString(lf)
	_, err := e.w.Write(buffer.Bytes())
	return err
}
