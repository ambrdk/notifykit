package telegramtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ambrdk/notifykit/telegram"
)

const (
	channel    = "-1001"
	discussion = int64(-1002)
	group      = "-1002"
)

func TestFakeAutoForward(t *testing.T) {
	f := New(channel, discussion)
	var seen []telegram.Update
	f.OnUpdate(func(u telegram.Update) { seen = append(seen, u) })
	ctx := context.Background()
	post, err := f.SendMessage(ctx, telegram.Message{ChatID: channel, Text: "post", Silent: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 {
		t.Fatalf("updates %d", len(seen))
	}
	m := seen[0].Message
	if m == nil || !m.IsAutomaticForward || m.ChatID != discussion || m.ForwardOriginMessageID != post || m.Date.IsZero() {
		t.Fatalf("copy %+v", m)
	}
	if got := f.Messages(group); len(got) != 1 || got[0].ID != m.MessageID || !got[0].Forwarded {
		t.Fatalf("discussion messages %+v", got)
	}
	if got := f.Messages(channel); len(got) != 1 || !got[0].Silent || got[0].Text != "post" {
		t.Fatalf("channel messages %+v", got)
	}

	f.HoldCopies(true)
	held, _ := f.SendMessage(ctx, telegram.Message{ChatID: channel, Text: "held"})
	if len(seen) != 1 {
		t.Fatal("held copy delivered")
	}
	f.ReleaseCopy(held)
	if len(seen) != 2 || seen[1].Message.ForwardOriginMessageID != held {
		t.Fatalf("released %+v", seen)
	}
}

func TestFakeWithoutDiscussion(t *testing.T) {
	f := New(channel, 0)
	if _, err := f.SendMessage(context.Background(), telegram.Message{ChatID: channel, Text: "post"}); err != nil {
		t.Fatal(err)
	}
	chat, err := f.GetChat(context.Background(), channel)
	if err != nil || chat.LinkedChatID != 0 {
		t.Fatalf("chat %+v err %v", chat, err)
	}
	if ups, _ := f.Poll(context.Background(), 0, 0); len(ups) != 0 {
		t.Fatalf("updates %d", len(ups))
	}
}

func TestFakeGoneAndNotModified(t *testing.T) {
	f := New(channel, discussion)
	ctx := context.Background()
	id, _ := f.SendMessage(ctx, telegram.Message{ChatID: channel, Text: "a"})
	if err := f.EditMessageText(ctx, channel, id, "a"); err != nil {
		t.Fatalf("not modified: %v", err)
	}
	if err := f.EditMessageText(ctx, channel, id, "b"); err != nil || f.Messages(channel)[0].Text != "b" {
		t.Fatalf("edit: %v", err)
	}
	f.Delete(channel, id)
	if err := f.EditMessageText(ctx, channel, id, "c"); !errors.Is(err, telegram.ErrMessageGone) {
		t.Fatalf("edit deleted: %v", err)
	}
	if err := f.EditMessageText(ctx, channel, 999, "c"); !errors.Is(err, telegram.ErrMessageGone) {
		t.Fatalf("edit unknown: %v", err)
	}
	if _, err := f.SendMessage(ctx, telegram.Message{ChatID: group, Text: "r", ReplyTo: 999}); !errors.Is(err, telegram.ErrMessageGone) {
		t.Fatalf("reply to unknown: %v", err)
	}
}

func TestFakeTooLong(t *testing.T) {
	f := New(channel, discussion)
	if _, err := f.SendMessage(context.Background(), telegram.Message{ChatID: channel, Text: strings.Repeat("я", 4097)}); err == nil || !strings.Contains(err.Error(), "message is too long") {
		t.Fatalf("err %v", err)
	}
	if _, err := f.SendMessage(context.Background(), telegram.Message{ChatID: channel, Text: strings.Repeat("я", 4096)}); err != nil {
		t.Fatalf("4096 runes: %v", err)
	}
}

func TestFakePollAck(t *testing.T) {
	f := New(channel, discussion)
	ctx := context.Background()
	for i := range 5 {
		f.Enqueue(telegram.Update{UpdateID: int64(i + 1)})
	}
	if ups, _ := f.Poll(ctx, 0, 0); len(ups) != 5 {
		t.Fatalf("first poll %d", len(ups))
	}
	if ups, _ := f.Poll(ctx, 4, 0); len(ups) != 2 || ups[0].UpdateID != 4 {
		t.Fatalf("offset poll %+v", ups)
	}
	if ups, _ := f.Poll(ctx, 0, 0); len(ups) != 2 {
		t.Fatalf("after ack %d", len(ups))
	}
	for i := range 150 {
		f.Enqueue(telegram.Update{UpdateID: int64(100 + i)})
	}
	if ups, _ := f.Poll(ctx, 0, 0); len(ups) != 100 {
		t.Fatalf("page %d", len(ups))
	}
}

func TestFakeFailChat(t *testing.T) {
	f := New(channel, discussion)
	ctx := context.Background()
	boom := errors.New("boom")
	f.FailChat(group, 1, boom)
	post, err := f.SendMessage(ctx, telegram.Message{ChatID: channel, Text: "p"})
	if err != nil {
		t.Fatalf("channel send failed: %v", err)
	}
	copyID := f.Messages(group)[0].ID
	if _, err := f.SendMessage(ctx, telegram.Message{ChatID: group, Text: "c", ReplyTo: copyID}); !errors.Is(err, boom) {
		t.Fatalf("group send: %v", err)
	}
	if _, err := f.SendMessage(ctx, telegram.Message{ChatID: group, Text: "c", ReplyTo: copyID}); err != nil {
		t.Fatalf("second group send: %v", err)
	}
	f.FailChat(channel, 1, boom)
	if err := f.EditMessageText(ctx, channel, post, "p2"); !errors.Is(err, boom) {
		t.Fatalf("channel edit: %v", err)
	}
}

func TestFakeRateLimit(t *testing.T) {
	f := New(channel, discussion)
	f.RateLimit(1, 3*time.Second)
	ctx := context.Background()
	_, err := f.SendMessage(ctx, telegram.Message{ChatID: channel, Text: "a"})
	var limited *telegram.RateLimitedError
	if !errors.As(err, &limited) || limited.RetryAfter != 3*time.Second {
		t.Fatalf("err %v", err)
	}
	if _, err := f.SendMessage(ctx, telegram.Message{ChatID: channel, Text: "a"}); err != nil {
		t.Fatalf("after limit: %v", err)
	}
	if f.Calls("sendMessage") != 2 {
		t.Fatalf("calls %d", f.Calls("sendMessage"))
	}
	f.FailGetChat(errors.New("down"))
	if _, err := f.GetChat(ctx, channel); err == nil {
		t.Fatal("getChat did not fail")
	}
	if _, err := f.GetChat(ctx, channel); err != nil {
		t.Fatalf("getChat after one failure: %v", err)
	}
}
