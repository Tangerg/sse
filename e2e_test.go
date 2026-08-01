package sse_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tangerg/sse"
)

// serve starts an httptest.Server whose handler is built by calling setup with
// an *sse.Writer. The server is closed automatically when the test ends.
func serve(t *testing.T, setup func(w *sse.Writer)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w := sse.NewHTTPWriter(rw)
		setup(w)
	}))
}

// connect opens an SSE connection to srv and returns an *sse.Reader.
func connect(t *testing.T, srv *httptest.Server) *sse.Reader {
	t.Helper()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET %s: %v", srv.URL, err)
	}
	cleanupResponse(t, resp)

	r, err := sse.NewHTTPReader(resp)
	if err != nil {
		t.Fatalf("NewHTTPReader: %v", err)
	}
	return r
}

func cleanupResponse(t *testing.T, resp *http.Response) {
	t.Helper()
	t.Cleanup(func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	})
}

// collectAll reads every message from r until the stream ends.
func collectAll(t *testing.T, r *sse.Reader) []sse.Message {
	t.Helper()
	var msgs []sse.Message
	for msg, err := range r.Messages() {
		if err != nil {
			t.Fatalf("Messages: %v", err)
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

func TestE2E_SingleMessage(t *testing.T) {
	srv := serve(t, func(w *sse.Writer) {
		if err := w.Write(sse.Message{ID: "1", Event: "greet", Data: []byte("hello")}); err != nil {
			return
		}
	})
	defer srv.Close()

	msgs := collectAll(t, connect(t, srv))

	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	m := msgs[0]
	if m.ID != "1" {
		t.Errorf("ID = %q, want %q", m.ID, "1")
	}
	if m.Event != "greet" {
		t.Errorf("Event = %q, want %q", m.Event, "greet")
	}
	if string(m.Data) != "hello" {
		t.Errorf("Data = %q, want %q", m.Data, "hello")
	}
}

func TestE2E_MultipleMessages(t *testing.T) {
	want := []sse.Message{
		{Data: []byte("first")},
		{Data: []byte("second")},
		{Data: []byte("third")},
	}

	srv := serve(t, func(w *sse.Writer) {
		for _, msg := range want {
			if err := w.Write(msg); err != nil {
				return
			}
		}
	})
	defer srv.Close()

	msgs := collectAll(t, connect(t, srv))

	if len(msgs) != len(want) {
		t.Fatalf("got %d messages, want %d", len(msgs), len(want))
	}
	for i, msg := range msgs {
		if string(msg.Data) != string(want[i].Data) {
			t.Errorf("msgs[%d].Data = %q, want %q", i, msg.Data, want[i].Data)
		}
	}
}

func TestE2E_AllFields(t *testing.T) {
	srv := serve(t, func(w *sse.Writer) {
		if err := w.Retry(2 * time.Second); err != nil {
			return
		}
		if err := w.Write(sse.Message{
			ID:    "42",
			Event: "update",
			Data:  []byte("payload"),
		}); err != nil {
			return
		}
	})
	defer srv.Close()

	r := connect(t, srv)
	msgs := collectAll(t, r)

	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	m := msgs[0]
	if m.ID != "42" {
		t.Errorf("ID = %q, want %q", m.ID, "42")
	}
	if m.Event != "update" {
		t.Errorf("Event = %q, want %q", m.Event, "update")
	}
	if string(m.Data) != "payload" {
		t.Errorf("Data = %q, want %q", m.Data, "payload")
	}
	if retry, ok := r.Retry(); !ok || retry != 2*time.Second {
		t.Errorf("Retry() = (%v, %v), want (2s, true)", retry, ok)
	}
}

func TestE2E_MultiLineData(t *testing.T) {
	srv := serve(t, func(w *sse.Writer) {
		if err := w.Write(sse.Message{Data: []byte("line1\nline2\nline3")}); err != nil {
			return
		}
	})
	defer srv.Close()

	msgs := collectAll(t, connect(t, srv))

	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if string(msgs[0].Data) != "line1\nline2\nline3" {
		t.Errorf("Data = %q, want %q", msgs[0].Data, "line1\nline2\nline3")
	}
}

func TestE2E_HeartbeatComment(t *testing.T) {
	srv := serve(t, func(w *sse.Writer) {
		if err := w.Comment("heartbeat"); err != nil {
			return
		}
		if err := w.Write(sse.Message{Data: []byte("after heartbeat")}); err != nil {
			return
		}
	})
	defer srv.Close()

	msgs := collectAll(t, connect(t, srv))

	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1 (comment must not dispatch a message)", len(msgs))
	}
	if string(msgs[0].Data) != "after heartbeat" {
		t.Errorf("Data = %q, want %q", msgs[0].Data, "after heartbeat")
	}
}

func TestE2E_IDPersistsAcrossEvents(t *testing.T) {
	srv := serve(t, func(w *sse.Writer) {
		if err := w.Write(sse.Message{ID: "7", Data: []byte("first")}); err != nil {
			return
		}
		if err := w.Write(sse.Message{Data: []byte("second")}); err != nil { // no id field
			return
		}
	})
	defer srv.Close()

	msgs := collectAll(t, connect(t, srv))

	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].ID != "7" {
		t.Errorf("msgs[0].ID = %q, want %q", msgs[0].ID, "7")
	}
	if msgs[1].ID != "7" {
		t.Errorf("msgs[1].ID = %q, want %q (id must persist from previous event)", msgs[1].ID, "7")
	}
}

func TestE2E_EarlyClientStop(t *testing.T) {
	started := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w := sse.NewHTTPWriter(rw)
		close(started)
		for {
			if err := w.Write(sse.Message{Data: []byte("tick")}); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()

	<-started

	r, err := sse.NewHTTPReader(resp)
	if err != nil {
		t.Fatal(err)
	}

	count := 0
	for _, err := range r.Messages() {
		if err != nil {
			t.Fatal(err)
		}
		count++
		if count == 3 {
			break
		}
	}

	if count != 3 {
		t.Errorf("got %d messages before stop, want 3", count)
	}
}

func TestE2E_JSONData(t *testing.T) {
	type orderEvent struct {
		OrderID string  `json:"order_id"`
		Status  string  `json:"status"`
		Total   float64 `json:"total"`
	}

	want := []orderEvent{
		{OrderID: "ord_1", Status: "confirmed", Total: 29.90},
		{OrderID: "ord_2", Status: "shipped", Total: 59.90},
		{OrderID: "ord_3", Status: "delivered", Total: 99.90},
	}

	srv := serve(t, func(w *sse.Writer) {
		for i, evt := range want {
			data, err := json.Marshal(evt)
			if err != nil {
				return
			}
			if err := w.Write(sse.Message{
				ID:    string(rune('1' + i)),
				Event: "order.updated",
				Data:  data,
			}); err != nil {
				return
			}
		}
	})
	defer srv.Close()

	msgs := collectAll(t, connect(t, srv))

	if len(msgs) != len(want) {
		t.Fatalf("got %d messages, want %d", len(msgs), len(want))
	}

	for i, msg := range msgs {
		if msg.Event != "order.updated" {
			t.Errorf("msgs[%d].Event = %q, want %q", i, msg.Event, "order.updated")
		}
		var got orderEvent
		if err := json.Unmarshal(msg.Data, &got); err != nil {
			t.Fatalf("msgs[%d]: json.Unmarshal: %v", i, err)
		}
		if got != want[i] {
			t.Errorf("msgs[%d] = %+v, want %+v", i, got, want[i])
		}
	}
}

func TestE2E_ResponseHeaders(t *testing.T) {
	srv := serve(t, func(w *sse.Writer) {})
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	cleanupResponse(t, resp)

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream; charset=utf-8")
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-cache")
	}
}
