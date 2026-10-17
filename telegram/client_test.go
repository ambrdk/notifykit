package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
)

const token = "123:secret-token"

type call struct {
	path   string
	fields map[string]string
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func fake(t *testing.T, status int, reply string, opts ...Option) (*Client, *[]call) {
	t.Helper()
	calls := &[]call{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fields := map[string]string{}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			for k, v := range r.MultipartForm.Value {
				fields[k] = v[0]
			}
		}
		*calls = append(*calls, call{path: r.URL.Path, fields: fields})
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	c, err := New(token, srv.URL, quiet(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c, calls
}

func TestSendMessageBody(t *testing.T) {
	c, calls := fake(t, 200, `{"ok":true,"result":{"message_id":55,"chat":{"id":-100}}}`)
	id, err := c.SendMessage(context.Background(), Message{ChatID: "-100", Text: "hi", Silent: true, ReplyTo: 9})
	if err != nil {
		t.Fatal(err)
	}
	if id != 55 {
		t.Fatalf("id %d", id)
	}
	got := (*calls)[0]
	if got.path != "/bot"+token+"/sendMessage" {
		t.Fatalf("path %s", got.path)
	}
	f := got.fields
	if f["chat_id"] != "-100" || f["text"] != "hi" || f["parse_mode"] != "HTML" || f["disable_notification"] != "true" {
		t.Fatalf("fields %v", f)
	}
	var reply map[string]any
	if err := json.Unmarshal([]byte(f["reply_parameters"]), &reply); err != nil || reply["message_id"] != float64(9) {
		t.Fatalf("reply %q", f["reply_parameters"])
	}
	assertNoPreview(t, f)
}

func assertNoPreview(t *testing.T, f map[string]string) {
	t.Helper()
	var preview map[string]any
	if err := json.Unmarshal([]byte(f["link_preview_options"]), &preview); err != nil || preview["is_disabled"] != true {
		t.Fatalf("link_preview_options %q", f["link_preview_options"])
	}
}

func TestSendMessageWithoutReply(t *testing.T) {
	c, calls := fake(t, 200, `{"ok":true,"result":{"message_id":1,"chat":{"id":-100}}}`)
	if _, err := c.SendMessage(context.Background(), Message{ChatID: "-100", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	f := (*calls)[0].fields
	if _, ok := f["reply_parameters"]; ok {
		t.Fatal("reply_parameters sent without ReplyTo")
	}
	if v, ok := f["disable_notification"]; ok && v != "false" {
		t.Fatalf("disable_notification %q", v)
	}
}

func TestEditMessageText(t *testing.T) {
	c, calls := fake(t, 200, `{"ok":true,"result":{"message_id":55,"chat":{"id":-100}}}`)
	if err := c.EditMessageText(context.Background(), "-100", 55, "new"); err != nil {
		t.Fatal(err)
	}
	got := (*calls)[0]
	if !strings.HasSuffix(got.path, "/editMessageText") || got.fields["message_id"] != "55" || got.fields["text"] != "new" || got.fields["parse_mode"] != "HTML" {
		t.Fatalf("call %+v", got)
	}
	assertNoPreview(t, got.fields)
}

func TestGetChatLinked(t *testing.T) {
	c, _ := fake(t, 200, `{"ok":true,"result":{"id":-1001,"type":"channel","linked_chat_id":-1002}}`)
	chat, err := c.GetChat(context.Background(), "-1001")
	if err != nil {
		t.Fatal(err)
	}
	if chat.ID != -1001 || chat.LinkedChatID != -1002 {
		t.Fatalf("chat %+v", chat)
	}
}

func TestConvertAutomaticForward(t *testing.T) {
	var u models.Update
	raw := `{"update_id":10,"message":{"message_id":3,"date":1,"chat":{"id":-1002,"type":"supergroup"},"is_automatic_forward":true,"forward_origin":{"type":"channel","date":1,"chat":{"id":-1001,"type":"channel"},"message_id":55}}}`
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	got := convert(&u)
	m := got.Message
	if got.UpdateID != 10 || m == nil || m.MessageID != 3 || m.ChatID != -1002 || !m.IsAutomaticForward || m.ForwardOriginMessageID != 55 {
		t.Fatalf("update %+v message %+v", got, m)
	}
	if convert(&models.Update{ID: 11}).Message != nil {
		t.Fatal("message from empty update")
	}
}

func TestListenDeliversUpdates(t *testing.T) {
	served := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !served {
			served = true
			_, _ = io.WriteString(w, `{"ok":true,"result":[{"update_id":7,"message":{"message_id":3,"date":1,"chat":{"id":-1002,"type":"supergroup"},"is_automatic_forward":true,"forward_origin":{"type":"channel","date":1,"chat":{"id":-1001,"type":"channel"},"message_id":55}}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
	}))
	defer srv.Close()
	c, err := New(token, srv.URL, quiet())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := make(chan Update, 1)
	go c.Listen(ctx, func(u Update) { got <- u; cancel() })
	select {
	case u := <-got:
		if u.Message == nil || u.Message.ForwardOriginMessageID != 55 {
			t.Fatalf("update %+v", u)
		}
	case <-ctx.Done():
		t.Fatal("no update delivered")
	}
}

func TestRateLimited(t *testing.T) {
	c, _ := fake(t, 429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`)
	_, err := c.SendMessage(context.Background(), Message{ChatID: "-100", Text: "hi"})
	var rl *RateLimitedError
	if !errors.As(err, &rl) || rl.RetryAfter != 7*time.Second {
		t.Fatalf("err %v", err)
	}
}

func TestNotModified(t *testing.T) {
	c, _ := fake(t, 400, `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified: specified new message content and reply markup are exactly the same"}`)
	if err := c.EditMessageText(context.Background(), "-100", 1, "same"); err != nil {
		t.Fatalf("err %v", err)
	}
}

func TestEditMessageGone(t *testing.T) {
	c, _ := fake(t, 400, `{"ok":false,"error_code":400,"description":"Bad Request: message to edit not found"}`)
	err := c.EditMessageText(context.Background(), "-100", 1, "new")
	if !errors.Is(err, ErrMessageGone) {
		t.Fatalf("err %v", err)
	}
}

func TestReplyTargetGone(t *testing.T) {
	c, _ := fake(t, 400, `{"ok":false,"error_code":400,"description":"Bad Request: message to be replied not found"}`)
	_, err := c.SendMessage(context.Background(), Message{ChatID: "-100", Text: "hi", ReplyTo: 9})
	if !errors.Is(err, ErrMessageGone) {
		t.Fatalf("err %v", err)
	}
}

func TestErrorHidesToken(t *testing.T) {
	c, err := New(token, "http://127.0.0.1:1", quiet())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SendMessage(context.Background(), Message{ChatID: "-100", Text: "hi"})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("token leaked: %v", err)
	}
}

func TestErrorHidesTokenOnBadURL(t *testing.T) {
	c, err := New(token, "http://bad host", quiet())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SendMessage(context.Background(), Message{ChatID: "-100", Text: "hi"})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("token leaked: %v", err)
	}
}

func TestPollingLogHidesToken(t *testing.T) {
	var buf safeBuffer
	seen := make(chan struct{}, 1)
	c, err := New(token, "http://bad host", slog.New(slog.NewTextHandler(&buf, nil)), WithObserver(func(Call) {
		select {
		case seen <- struct{}{}:
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
	case <-seen:
	case <-ctx.Done():
		t.Fatal("no polling error")
	}
	cancel()
	<-done
	if out := buf.String(); out == "" || strings.Contains(out, "secret-token") {
		t.Fatalf("log %q", out)
	}
}

type safeBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
