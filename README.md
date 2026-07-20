# sse

A small Go library for reading and writing
[Server-Sent Events (SSE)](https://html.spec.whatwg.org/multipage/server-sent-events.html).

It covers the core parsing and serialisation rules of the WHATWG HTML Living
Standard §9.2 — BOM stripping, all three line endings (LF / CR / CRLF), the four
fields (`data`, `event`, `id`, `retry`), the single-leading-space rule, and
blank-line dispatch. It is a codec, not an EventSource client: automatic
reconnection and request retries are the caller's responsibility.

## Requirements

Go 1.23 or later.

## Installation

```sh
go get github.com/Tangerg/sse
```

## Writing

### HTTP server

`NewHTTPWriter` sets the SSE response headers and flushes each event to the
client immediately:

```go
func handler(w http.ResponseWriter, r *http.Request) {
    sw := sse.NewHTTPWriter(w)

    if err := sw.Write(sse.Message{
        ID:    "1",
        Event: "update",
        Data:  []byte("hello world"),
    }); err != nil {
        return // client disconnected
    }

    // Heartbeat every ~15s to keep idle proxies from closing the connection.
    sw.Comment("keep-alive")
}
```

Headers set by `NewHTTPWriter`:

| Header          | Value                              |
|-----------------|------------------------------------|
| `Content-Type`  | `text/event-stream; charset=utf-8` |
| `Cache-Control` | `no-cache` *(only if unset)*       |

No `Connection` header is set: it is a hop-by-hop header that HTTP/2 forbids.
Flushing goes through `http.ResponseController`, so it works through middleware
that wraps the `ResponseWriter`.

### Plain `io.Writer`

```go
sw := sse.NewWriter(w)

if err := sw.Retry(5 * time.Second); err != nil {
    return err
}
err := sw.Write(sse.Message{
    Event: "ping",
    Data:  []byte("{}"),
})
```

Every `Write` call emits a dispatchable event — a single `data:` line is
written even when `Data` is empty, so an event carrying only a type (e.g. a
refresh signal) is expressible. All fields must be valid UTF-8. An `ID`
containing CR, LF, or NUL, or an `Event` containing CR or LF, is rejected with
an error rather than silently altered.

`Retry` and `ResetID` emit standalone control frames — a new reconnection time,
or a reset of the last event ID — without dispatching an event:

```go
sw.Retry(10 * time.Second) // retry: 10000
sw.ResetID()               // id:
```

## Reading

### HTTP client

`NewHTTPReader` validates the 200 response status and response media type (with
`mime.ParseMediaType`) before parsing:

```go
resp, err := http.Get("https://example.com/events")
if err != nil { ... }
defer resp.Body.Close()

sr, err := sse.NewHTTPReader(resp)
if err != nil { ... }

for msg, err := range sr.Messages() {
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("event=%s data=%s\n", msg.Event, msg.Data)
}
```

A clean end of stream ends the loop without an error; malformed UTF-8 is decoded
as `U+FFFD` per the Encoding Standard. A non-nil error means an I/O failure, a
line exceeding the buffer limit, or `ErrEventTooLarge`. There is no context
parameter: to interrupt a read blocked on a stalled connection, close
`resp.Body`; to stop consuming, break out of the loop.

After the loop ends, `LastEventID()` and `Retry()` report the reconnection state
— including values from standalone `id:` or `retry:` frames that carried no
event — so a caller can reconnect:

```go
if id := sr.LastEventID(); id != "" {
    req.Header.Set("Last-Event-ID", id)
} else {
    req.Header.Del("Last-Event-ID")
}
if d, ok := sr.Retry(); ok {
    time.Sleep(d)
}
```

### Plain `io.Reader`

```go
sr := sse.NewReader(r)
for msg, err := range sr.Messages() {
    ...
}
```

### Large payloads

The default per-line limit is 64 KiB. Raise `MaxLineBytes` before the first call
to `Messages` when a single `data` field may be larger (e.g. a serialised JSON
object):

```go
sr := sse.NewReader(r)
sr.MaxLineBytes = 512 * 1024 // 512 KiB per line
```

A line exceeding the limit makes `Messages` yield a non-nil error. For untrusted
streams, also set `MaxEventBytes` to bound the total data buffered for a single
event (many small `data:` lines could otherwise grow without limit); exceeding it
yields `ErrEventTooLarge`.

### JSON data

`Message.Data` is `[]byte`, so pass `json.Marshal` output directly and
`json.Unmarshal` on receipt:

```go
payload, _ := json.Marshal(OrderEvent{OrderID: "ord_123", Status: "shipped"})
sw.Write(sse.Message{ID: "1", Event: "order.updated", Data: payload})

for msg, err := range sr.Messages() {
    if err != nil { log.Fatal(err) }
    var evt OrderEvent
    if err := json.Unmarshal(msg.Data, &evt); err != nil { log.Fatal(err) }
}
```

## API

```go
type Message struct {
    ID    string
    Event string
    Data  []byte
}

func NewReader(r io.Reader) *Reader
func NewHTTPReader(resp *http.Response) (*Reader, error)
func (r *Reader) Messages() iter.Seq2[Message, error]
func (r *Reader) LastEventID() string
func (r *Reader) Retry() (time.Duration, bool)
// Reader.MaxLineBytes int  — per-line limit; 0 uses the 64 KiB default.
// Reader.MaxEventBytes int — per-event data limit; 0 means unlimited.

func NewWriter(w io.Writer) *Writer
func NewHTTPWriter(rw http.ResponseWriter) *Writer
func (w *Writer) Write(msg Message) error
func (w *Writer) Comment(comment string) error
func (w *Writer) Retry(delay time.Duration) error
func (w *Writer) ResetID() error
```

| Field   | Wire field | Notes |
|---------|-----------|-------|
| `ID`    | `id`      | Last-event-ID. Persists across events on read; received values containing NUL are ignored. On write an empty ID omits the line. |
| `Event` | `event`   | Defaults to `"message"` when absent. On write an empty value omits the line. |
| `Data`  | `data`    | Multi-line values are one `data:` line each; joined with LF on read. An empty `Data` still emits one `data:` line so the event dispatches. |

## Spec compliance

Implements the core parsing/serialisation of WHATWG HTML Living Standard §9.2:
<https://html.spec.whatwg.org/multipage/server-sent-events.html>

| Requirement | §9.2 reference |
|---|---|
| Leading UTF-8 BOM stripped once | §9.2.6 |
| Malformed UTF-8 decoded with U+FFFD replacement | §9.2.6 / Encoding Standard |
| All three line endings: LF, CR, CRLF | §9.2.5 `end-of-line` |
| One leading space after `:` stripped from values | §9.2.6 |
| `id` values containing NUL ignored on read; rejected on write | §9.2.6 |
| `retry` values with non-digit characters ignored; overflow clamped | §9.2.6 |
| Trailing LF removed from the data buffer on dispatch | §9.2.6 |
| Events with an empty data buffer discarded on read | §9.2.6 |
| Last-event-ID and retry persist across events | §9.2.6 |
| Incomplete final event (no trailing blank line) discarded | §9.2.6 |
| HTTP 200 status and media type `text/event-stream` enforced on read | §9.2.3 / §9.2.5 |

Not implemented (out of scope): EventSource connection establishment and
automatic reconnection. The package exposes the parsed last-event ID and retry
delay so callers can implement that policy themselves.

## License

MIT
