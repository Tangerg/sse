package sse

const (
	lf    = "\n"
	cr    = "\r"
	space = " "
	colon = ":"
	bom   = "\uFEFF"
	null  = "\x00"
)

const (
	fieldID    = "id"
	fieldEvent = "event"
	fieldData  = "data"
	fieldRetry = "retry"
)

const (
	defaultEvent = "message"
)

type Message struct {
	ID    string
	Event string
	Data  []byte
	Retry int
}
