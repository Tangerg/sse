# sse

A minimal, spec-compliant Go library for reading and writing
[Server-Sent Events (SSE)](https://html.spec.whatwg.org/multipage/server-sent-events.html).

Implements the WHATWG HTML Living Standard §9.2 in full — BOM stripping,
all three line endings (LF / CR / CRLF), all four field names (`data`, `event`,
`id`, `retry`), and correct blank-line dispatch semantics.

## Requirements

Go 1.23 or later (uses `iter.Seq2` from the standard library introduced in Go 1.23).

## Installation

```sh
go get github.com/Tangerg/sse
```

## Usage

### Writing events — HTTP server

`NewHTTPWriter` sets the required response headers and flushes each event to
the client immediately:

```go
func handler(w http.ResponseWriter, r *http.Request) {
    sw, err := sse.NewHTTPWriter(w)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    if err := sw.Message(r.Context(), sse.Message{
        ID:    "1",
        Event: "update",
        Data:  []byte("hello world"),
    }); err != nil {
        return // client disconnected
    }

    // Send a heartbeat comment every ~15 s to prevent proxy timeouts (§9.2.7).
    sw.Comment(r.Context(), "keep-alive")
}
```

Headers set automatically by `NewHTTPWriter`:

| Header          | Value                              |
|-----------------|------------------------------------|
| `Content-Type`  | `text/event-stream; charset=utf-8` |
| `Connection`    | `keep-alive`                       |
| `Cache-Control` | `no-cache` *(if not already set)*  |

### Writing events — plain `io.Writer`

```go
sw := sse.NewWriter(w)

if err := sw.Message(ctx, sse.Message{
    Event: "ping",
    Data:  []byte("{}"),
    Retry: 5 * time.Second,
}); err != nil { ... }
```

### Reading events — HTTP client

`NewHTTPReader` validates the `Content-Type` header before parsing:

```go
resp, err := http.Get("https://example.com/events")
if err != nil { ... }
defer resp.Body.Close()

sr, err := sse.NewHTTPReader(resp)
if err != nil { ... }

for msg, err := range sr.Messages(ctx) {
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("event=%s data=%s\n", msg.Event, msg.Data)
}
```

The error value is non-nil only on context cancellation or an I/O error; normal
end-of-stream is not reported as an error. Context cancellation is cooperative
(checked between scans). To unblock a scan waiting on a stalled connection,
close `resp.Body`.

### Reading events — plain `io.Reader`

```go
sr := sse.NewReader(r)

for msg, err := range sr.Messages(ctx) {
    ...
}
```

### Large payloads

The scanner's default per-line limit is 64 KiB. Pass an explicit buffer size
(in bytes) to either constructor when the stream may carry larger payloads in a
single `data` field (e.g. serialised JSON objects):

```go
sr, err := sse.NewHTTPReader(resp, 512*1024) // 512 KiB per line
sr        := sse.NewReader(r,    512*1024)
```

Lines that exceed the configured limit cause `Messages` to yield a non-nil
error.

### JSON data

`Message.Data` is `[]byte`, so pass the output of `json.Marshal` directly:

```go
// Server — writing
payload, _ := json.Marshal(OrderEvent{OrderID: "ord_123", Status: "shipped"})
if err := sw.Message(ctx, sse.Message{
    ID:    "1",
    Event: "order.updated",
    Data:  payload,
}); err != nil { ... }

// Client — reading
for msg, err := range sr.Messages(ctx) {
    if err != nil { log.Fatal(err) }
    var evt OrderEvent
    if err := json.Unmarshal(msg.Data, &evt); err != nil { log.Fatal(err) }
    fmt.Printf("order %s is now %s\n", evt.OrderID, evt.Status)
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

| Field   | Wire field | Notes |
|---------|-----------|-------|
| `ID`    | `id`      | Persists across events until the server resets it. Values containing U+0000 are ignored on receipt; an empty ID omits the field on write. |
| `Event` | `event`   | Defaults to `"message"` when absent from the stream. An empty value omits the field on write. |
| `Data`  | `data`    | Multi-line values are split into one `data:` line each. Events with no data are discarded (§9.2.6). |
| `Retry` | `retry`   | Reconnection-time hint, converted to/from milliseconds. Zero or negative omits the field on write. |

### `Writer`

| Constructor / Method | Description |
|---|---|
| `NewWriter(w io.Writer) *Writer` | Writer for any `io.Writer`. Panics if w is nil. |
| `NewHTTPWriter(rw http.ResponseWriter) (*Writer, error)` | Writer for HTTP; sets SSE headers and flushes after each write. |
| `(*Writer).Message(ctx context.Context, msg Message) error` | Encode and write one SSE event frame. Returns immediately if ctx is done. |
| `(*Writer).Comment(ctx context.Context, comment string) error` | Write an SSE comment line. Ignored by receivers but keeps the connection alive through proxies (§9.2.7). Returns immediately if ctx is done. |

### `Reader`

| Constructor / Method | Description |
|---|---|
| `NewReader(r io.Reader, bufSize ...int) *Reader` | Reader for any `io.Reader`. Panics if r is nil. No I/O on construction. Optional `bufSize` overrides the scanner's default 64 KiB per-line limit. |
| `NewHTTPReader(resp *http.Response, bufSize ...int) (*Reader, error)` | Reader from an HTTP response; validates `Content-Type: text/event-stream`. Optional `bufSize` is forwarded to `NewReader`. |
| `(*Reader).Messages(ctx context.Context) iter.Seq2[Message, error]` | Iterator over all dispatched events. The scanner is initialised lazily on the first call and reused on subsequent calls. Normal end-of-stream yields no error. Non-nil error means context cancellation, I/O failure, or a line exceeding the buffer limit. To cancel a blocked read, close the underlying reader. |

## Spec compliance

Implements WHATWG HTML Living Standard §9.2:
<https://html.spec.whatwg.org/multipage/server-sent-events.html>

| Requirement | §9.2 reference |
|---|---|
| UTF-8 BOM stripped from stream start | §9.2.6 — "UTF-8 decode algorithm strips one leading BOM" |
| All three line endings: LF, CR, CRLF | §9.2.5 `end-of-line` production |
| Single leading space after `:` stripped from field values | §9.2.6 — "If value starts with U+0020 SPACE, remove it" |
| `id` values containing U+0000 ignored | §9.2.6 — "If the field value does not contain U+0000 NULL…" |
| `retry` values with non-ASCII-digit characters ignored | §9.2.6 — "If the field value consists of only ASCII digits…" |
| Trailing LF removed from data buffer on dispatch | §9.2.6 dispatch step 3 |
| Events with empty data buffer discarded | §9.2.6 dispatch step 2 |
| Last-event-ID persists across events | §9.2.6 dispatch step 1 — "The buffer does not get reset" |
| Incomplete final event (no trailing blank line) discarded | §9.2.6 — "any pending data must be discarded" |
| MIME type `text/event-stream` enforced on read | §9.2.5 |

## License

MIT
