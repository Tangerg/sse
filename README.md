# sse

A minimal, spec-compliant Go library for reading and writing
[Server-Sent Events (SSE)](https://html.spec.whatwg.org/multipage/server-sent-events.html).

Implements the WHATWG HTML Living Standard §9.2 in full:
BOM stripping, all three line endings (LF / CR / CRLF), all four field names
(`data`, `event`, `id`, `retry`), and correct blank-line dispatch semantics.

## Installation

```sh
go get github.com/Tangerg/sse
```

## Usage

### Writing events (HTTP server)

```go
func handler(w http.ResponseWriter, r *http.Request) {
    writer, err := sse.NewHTTPWriter(w)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // Send a named event with data.
    writer.Message(sse.Message{
        ID:    "1",
        Event: "update",
        Data:  []byte("hello world"),
    })

    // Send a heartbeat comment every ~15 s to prevent proxy timeouts.
    // SSE comments (lines starting with ':') are ignored by the client
    // but keep the connection alive through intermediary proxies.
    writer.Comment("heartbeat")
}
```

`NewHTTPWriter` sets the required response headers automatically:

| Header          | Value                              |
|-----------------|------------------------------------|
| `Content-Type`  | `text/event-stream; charset=utf-8` |
| `Connection`    | `keep-alive`                       |
| `Cache-Control` | `no-cache` (if not already set)    |

### Writing events (plain io.Writer)

```go
writer, err := sse.NewWriter(w)
if err != nil { ... }

writer.Message(sse.Message{
    Event: "ping",
    Data:  []byte("{}"),
    Retry: 5 * time.Second,
})
```

### Reading events (HTTP client)

```go
resp, err := http.Get("https://example.com/events")
if err != nil { ... }
defer resp.Body.Close()

reader, err := sse.NewHTTPReader(resp)
if err != nil { ... }

for msg, err := range reader.Messages() {
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("event=%s data=%s\n", msg.Event, msg.Data)
}
```

### Writing JSON data

`Message.Data` is `[]byte`, so pass the output of `json.Marshal` directly:

```go
type OrderEvent struct {
    OrderID string  `json:"order_id"`
    Status  string  `json:"status"`
    Total   float64 `json:"total"`
}

func handler(w http.ResponseWriter, r *http.Request) {
    writer, err := sse.NewHTTPWriter(w)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    payload, err := json.Marshal(OrderEvent{
        OrderID: "ord_123",
        Status:  "shipped",
        Total:   59.90,
    })
    if err != nil {
        return
    }

    writer.Message(sse.Message{
        ID:    "1",
        Event: "order.updated",
        Data:  payload,
    })
}
```

On the client side, unmarshal `msg.Data` the same way:

```go
for msg, err := range reader.Messages() {
    if err != nil {
        log.Fatal(err)
    }

    var evt OrderEvent
    if err := json.Unmarshal(msg.Data, &evt); err != nil {
        log.Fatal(err)
    }

    fmt.Printf("order %s is now %s\n", evt.OrderID, evt.Status)
}
```

### Reading events (plain io.Reader)

```go
reader, err := sse.NewReader(r)
if err != nil { ... }

for msg, err := range reader.Messages() {
    ...
}
```

## API

### `Message`

```go
type Message struct {
    ID    string
    Event string
    Data  []byte
    Retry time.Duration
}
```

| Field   | SSE field | Notes                                          |
|---------|-----------|------------------------------------------------|
| `ID`    | `id`      | Omitted if empty. Ignored if it contains NUL. |
| `Event` | `event`   | Defaults to `"message"` on read when absent.  |
| `Data`  | `data`    | Multi-line values are split automatically.     |
| `Retry` | `retry`   | Omitted if ≤ 0.                               |

### `Writer`

| Method                           | Description                          |
|----------------------------------|--------------------------------------|
| `NewWriter(w io.Writer)`         | Create a writer for any `io.Writer`. |
| `NewHTTPWriter(rw http.ResponseWriter)` | Create a writer for HTTP, sets SSE headers and flushes after each write. |
| `(*Writer).Message(msg Message)` | Write one SSE event frame.           |
| `(*Writer).Comment(comment string)` | Write an SSE comment line. Ignored by clients but keeps the connection alive through proxies — use for heartbeats. |

### `Reader`

| Method                                    | Description                                   |
|-------------------------------------------|-----------------------------------------------|
| `NewReader(r io.Reader)`                  | Create a reader for any `io.Reader`.          |
| `NewHTTPReader(resp *http.Response)`      | Create a reader from an HTTP response; validates `Content-Type`. |
| `(*Reader).Messages() iter.Seq2[Message, error]` | Iterate over all dispatched events.   |

## Spec compliance

Follows the WHATWG HTML Living Standard §9.2:
<https://html.spec.whatwg.org/multipage/server-sent-events.html>

- UTF-8 BOM stripped from the start of the stream
- All three line endings accepted: LF, CR, CRLF
- Leading space after `:` stripped from field values
- `id` fields containing U+0000 are ignored
- `retry` fields containing non-ASCII-digit characters are ignored
- Trailing LF removed from the data buffer on dispatch
- Incomplete final events (no trailing blank line) are discarded

## License

MIT
