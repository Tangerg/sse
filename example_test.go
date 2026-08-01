package sse_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tangerg/sse"
)

// Example reads a short event stream and prints each dispatched event. Multiple
// data lines in one event are joined with a newline.
func Example() {
	stream := "event: greeting\ndata: hello\n\n" +
		"data: line one\ndata: line two\n\n"

	r := sse.NewReader(strings.NewReader(stream))
	for msg, err := range r.Messages() {
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Printf("[%s] %q\n", msg.Event, msg.Data)
	}
	// Output:
	// [greeting] "hello"
	// [message] "line one\nline two"
}

// ExampleReader_Read reads one event at a time, following the usual stdlib
// Reader convention of reporting a clean end of input as io.EOF.
func ExampleReader_Read() {
	r := sse.NewReader(strings.NewReader("data: first\n\ndata: second\n\n"))
	for {
		msg, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Println(string(msg.Data))
	}
	// Output:
	// first
	// second
}

// ExampleWriter encodes one event. The frame ends with a blank line, which is
// what tells the receiver to dispatch the event.
func ExampleWriter() {
	var buf bytes.Buffer
	w := sse.NewWriter(&buf)
	if err := w.Write(sse.Message{ID: "42", Event: "update", Data: []byte("payload")}); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("%q", buf.String())
	// Output: "id: 42\nevent: update\ndata: payload\n\n"
}

// ExampleWriter_multiline shows that a Data value containing newlines is split
// into one data line each; the receiver rejoins them with a newline.
func ExampleWriter_multiline() {
	var buf bytes.Buffer
	w := sse.NewWriter(&buf)
	if err := w.Write(sse.Message{Data: []byte("first\nsecond")}); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("%q", buf.String())
	// Output: "data: first\ndata: second\n\n"
}

// ExampleWriter_Comment writes a comment line. Receivers ignore comments; a
// periodic comment keeps idle proxies from closing the connection.
func ExampleWriter_Comment() {
	var buf bytes.Buffer
	w := sse.NewWriter(&buf)
	if err := w.Comment("keep-alive"); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("%q", buf.String())
	// Output: ": keep-alive\n"
}

// ExampleWriter_Retry writes a standalone control frame that sets the client's
// reconnection time without dispatching an event.
func ExampleWriter_Retry() {
	var buf bytes.Buffer
	w := sse.NewWriter(&buf)
	if err := w.Retry(3 * time.Second); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("%q", buf.String())
	// Output: "retry: 3000\n\n"
}

// ExampleReader_Retry recovers the reconnection state after the stream ends —
// including a retry that arrived in its own frame carrying no event.
func ExampleReader_Retry() {
	stream := "id: 7\ndata: tick\n\nretry: 5000\n\n"

	r := sse.NewReader(strings.NewReader(stream))
	for _, err := range r.Messages() {
		if err != nil {
			fmt.Println("error:", err)
			return
		}
	}

	retry, ok := r.Retry()
	fmt.Printf("last id = %q, retry = %v (set: %t)\n", r.LastEventID(), retry, ok)
	// Output: last id = "7", retry = 5s (set: true)
}

// ExampleNewHTTPWriter shows an SSE endpoint. NewHTTPWriter sets the response
// headers and flushes each frame to the client immediately.
func ExampleNewHTTPWriter() {
	handler := func(w http.ResponseWriter, r *http.Request) {
		sw := sse.NewHTTPWriter(w)
		if err := sw.Write(sse.Message{Event: "update", Data: []byte("hello")}); err != nil {
			return // client disconnected
		}
		_ = sw.Comment("keep-alive") // periodic heartbeat
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", handler)
}

// ExampleNewHTTPReader consumes an SSE response. NewHTTPReader validates that
// the response is 200 OK with a text/event-stream media type.
func ExampleNewHTTPReader() {
	resp, err := http.Get("https://example.com/events")
	if err != nil {
		return
	}
	defer resp.Body.Close()

	r, err := sse.NewHTTPReader(resp)
	if err != nil {
		return
	}
	for msg, err := range r.Messages() {
		if err != nil {
			break
		}
		fmt.Printf("%s: %s\n", msg.Event, msg.Data)
	}
}
