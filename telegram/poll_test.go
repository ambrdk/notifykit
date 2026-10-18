package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func pollServer(t *testing.T, status int, reply string, got *map[string]any, path *string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, got)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestPollSendsOffsetAndTimeout(t *testing.T) {
	var got map[string]any
	var path string
	url := pollServer(t, 200, `{"ok":true,"result":[{"update_id":7,"message":{"message_id":3,"date":1,"chat":{"id":-1002,"type":"supergroup"},"is_automatic_forward":true,"forward_origin":{"type":"channel","date":1,"chat":{"id":-1001,"type":"channel"},"message_id":55}}}]}`, &got, &path)
	c, err := New(token, url, quiet())
	if err != nil {
		t.Fatal(err)
	}
	updates, err := c.Poll(context.Background(), 42, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/bot"+token+"/getUpdates" {
		t.Fatalf("path %s", path)
	}
	if got["offset"] != float64(42) || got["timeout"] != float64(5) {
		t.Fatalf("body %v", got)
	}
	allowed, _ := got["allowed_updates"].([]any)
	if !slices.Contains(allowed, any("message")) {
		t.Fatalf("allowed_updates %v", got["allowed_updates"])
	}
	if len(updates) != 1 || updates[0].UpdateID != 7 || updates[0].Message == nil || updates[0].Message.ForwardOriginMessageID != 55 {
		t.Fatalf("updates %+v", updates)
	}
}

func TestPollTranslatesErrors(t *testing.T) {
	var got map[string]any
	var path string
	url := pollServer(t, 429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 3","parameters":{"retry_after":3}}`, &got, &path)
	rec := &recorder{}
	c, err := New(token, url, quiet(), WithObserver(rec.observe))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Poll(context.Background(), 0, 0)
	var rl *RateLimitedError
	if !errors.As(err, &rl) || rl.RetryAfter != 3*time.Second {
		t.Fatalf("err %v", err)
	}
	calls := rec.all()
	if len(calls) != 1 || calls[0].Method != "getUpdates" || !errors.As(calls[0].Err, &rl) {
		t.Fatalf("calls %+v", calls)
	}
}

func TestPollHidesToken(t *testing.T) {
	c, err := New(token, "http://bad host", quiet())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Poll(context.Background(), 0, 0)
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("err %v", err)
	}
}
