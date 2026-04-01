package sse

const (
	lf    = "\u000A"
	cr    = "\u000D"
	space = "\u0020"
	colon = "\u003A"
	bom   = "\uFEFF"
	null  = "\u0000"
)

const (
	fieldID    = "id"
	fieldEvent = "event"
	fieldData  = "data"
	fieldRetry = "retry"
)

const (
	eventMessage = "message"
)

type Message struct {
	ID    string
	Event string
	Data  []byte
	Retry int
}
