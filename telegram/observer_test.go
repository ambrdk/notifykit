package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recorder struct {
	mu    sync.Mutex
	calls []Call
}

func (r *recorder) observe(c Call) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
}

func (r *recorder) all() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Call(nil), r.calls...)
}

func TestObserverSeesEachCall(t *testing.T) {
	rec := &recorder{}
	c, _ := fake(t, 200, `{"ok":true,"result":{"message_id":55,"id":-1001,"type":"channel","chat":{"id":-100}}}`, WithObserver(rec.observe))
	ctx := context.Background()
	if _, err := c.SendMessage(ctx, Message{ChatID: "-100", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := c.EditMessageText(ctx, "-100", 55, "new"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetChat(ctx, "-1001"); err != nil {
		t.Fatal(err)
	}
	got := rec.all()
	want := []string{"sendMessage", "editMessageText", "getChat"}
	if len(got) != len(want) {
		t.Fatalf("calls %+v", got)
	}
	for i, m := range want {
		if got[i].Method != m || got[i].Err != nil || got[i].Duration < 0 {
			t.Fatalf("call %d %+v", i, got[i])
		}
	}
}

func TestObserverGetsTranslatedErrors(t *testing.T) {
	rec := &recorder{}
	c, _ := fake(t, 400, `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified: specified new message content and reply markup are exactly the same"}`, WithObserver(rec.observe))
	_ = c.EditMessageText(context.Background(), "-100", 1, "same")
	limited, _ := fake(t, 429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`, WithObserver(rec.observe))
	_, _ = limited.SendMessage(context.Background(), Message{ChatID: "-100", Text: "hi"})
	got := rec.all()
	if len(got) != 2 {
		t.Fatalf("calls %+v", got)
	}
	if got[0].Method != "editMessageText" || got[0].Err != nil {
		t.Fatalf("not modified call %+v", got[0])
	}
	var rl *RateLimitedError
	if got[1].Method != "sendMessage" || !errors.As(got[1].Err, &rl) {
		t.Fatalf("rate limited call %+v", got[1])
	}
}

func TestObserverPollingError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":500,"description":"Internal Server Error"}`)
	}))
	defer srv.Close()
	seen := make(chan Call, 16)
	c, err := New(token, srv.URL, quiet(), WithObserver(func(call Call) {
		select {
		case seen <- call:
		default:
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { c.Listen(ctx, func(Update) {}); close(done) }()
	select {
	case call := <-seen:
		if call.Method != "getUpdates" || call.Err == nil {
			t.Fatalf("call %+v", call)
		}
	case <-ctx.Done():
		t.Fatal("polling error not observed")
	}
	cancel()
	<-done
}

func TestObserverQuietWhenShutdownDropsUpdates(t *testing.T) {
	var batch strings.Builder
	batch.WriteString(`{"ok":true,"result":[`)
	for i := 1; i <= 1500; i++ {
		if i > 1 {
			batch.WriteString(",")
		}
		fmt.Fprintf(&batch, `{"update_id":%d,"message":{"message_id":%d,"date":1,"chat":{"id":-1002,"type":"supergroup"}}}`, i, i)
	}
	batch.WriteString(`]}`)
	var served atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if served.CompareAndSwap(false, true) {
			_, _ = io.WriteString(w, batch.String())
			return
		}
		<-r.Context().Done()
	}))
	defer srv.Close()
	rec := &recorder{}
	c, err := New(token, srv.URL, quiet(), WithObserver(rec.observe))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { c.Listen(ctx, func(Update) { cancel() }); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Listen did not return")
	}
	for _, call := range rec.all() {
		if call.Method == "getUpdates" && call.Err != nil {
			t.Fatalf("shutdown reported as error: %+v", call)
		}
	}
}

func TestObserverQuietAfterShutdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer srv.Close()
	rec := &recorder{}
	c, err := New(token, srv.URL, quiet(), WithObserver(rec.observe))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Listen(ctx, func(Update) {}); close(done) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Listen did not return")
	}
	for _, call := range rec.all() {
		if call.Method == "getUpdates" && call.Err != nil {
			t.Fatalf("shutdown reported as error: %+v", call)
		}
	}
}
