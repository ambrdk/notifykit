package thread

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ambrdk/notifykit/telegram"
	"github.com/ambrdk/notifykit/telegram/telegramtest"
)

type clock struct {
	t      time.Time
	slept  time.Duration
	sleeps int
}

func (c *clock) now() time.Time { return c.t }

func (c *clock) sleep(_ context.Context, d time.Duration) error {
	c.t = c.t.Add(d)
	c.slept += d
	c.sleeps++
	return nil
}

func waitingWith(f *telegramtest.Fake, wait time.Duration) (*waiting, *clock) {
	c := &clock{t: time.Now()}
	w := Waiting(f, wait).(*waiting)
	w.now, w.sleep = c.now, c.sleep
	return w, c
}

func send(t *testing.T, f *telegramtest.Fake, text string) int64 {
	t.Helper()
	id, err := f.SendMessage(context.Background(), telegram.Message{ChatID: channel, Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestWaitingFindsCopy(t *testing.T) {
	f := telegramtest.New(channel, discussion)
	w, c := waitingWith(f, 30*time.Second)
	post := send(t, f, "p")
	id, pending, err := w.Find(context.Background(), discussion, post)
	if err != nil || pending || id == 0 || c.sleeps != 0 {
		t.Fatalf("id %d pending %v err %v sleeps %d", id, pending, err, c.sleeps)
	}
	if id != f.Messages(group)[0].ID {
		t.Fatalf("copy %d, discussion %+v", id, f.Messages(group))
	}
}

func TestWaitingLeavesForeignUpdates(t *testing.T) {
	f := telegramtest.New(channel, discussion)
	w, _ := waitingWith(f, 30*time.Second)
	send(t, f, "other")
	post := send(t, f, "p")
	if id, _, _ := w.Find(context.Background(), discussion, post); id == 0 {
		t.Fatal("copy not found")
	}
	if ups, _ := f.Poll(context.Background(), 0, 0); len(ups) != 2 {
		t.Fatalf("queue after find %d, want both updates kept", len(ups))
	}
}

func TestWaitingLateCopyFoundNextRun(t *testing.T) {
	f := telegramtest.New(channel, discussion)
	f.HoldCopies(true)
	w, _ := waitingWith(f, 30*time.Second)
	post := send(t, f, "p")
	if id, pending, err := w.Find(context.Background(), discussion, post); id != 0 || pending || err != nil {
		t.Fatalf("held copy found: id %d pending %v err %v", id, pending, err)
	}
	f.ReleaseCopy(post)
	other := send(t, f, "next release")
	f.HoldCopies(false)
	next, _ := waitingWith(f, 30*time.Second)
	if id, _, _ := next.Find(context.Background(), discussion, other); id != 0 {
		t.Fatalf("held post is still held, found %d", id)
	}
	if id, _, _ := next.Find(context.Background(), discussion, post); id == 0 {
		t.Fatal("late copy lost")
	}
}

func TestWaitingAcksOldPrefix(t *testing.T) {
	f := telegramtest.New(channel, discussion)
	w := Waiting(f, 30*time.Second).(*waiting)
	old := time.Now().Add(-2 * time.Hour)
	for i := range 120 {
		f.Enqueue(telegram.Update{UpdateID: int64(i + 1), Message: &telegram.ChatMessage{MessageID: int64(i + 1), ChatID: discussion, Date: old}})
	}
	post := send(t, f, "p")
	id, _, err := w.Find(context.Background(), discussion, post)
	if err != nil || id == 0 {
		t.Fatalf("id %d err %v", id, err)
	}
	ups, _ := f.Poll(context.Background(), 0, 0)
	if len(ups) != 1 || ups[0].Message.ForwardOriginMessageID != post {
		t.Fatalf("queue %d, want only the fresh copy", len(ups))
	}
}

func TestWaitingDeadline(t *testing.T) {
	f := telegramtest.New(channel, discussion)
	f.HoldCopies(true)
	w, c := waitingWith(f, 30*time.Second)
	post := send(t, f, "p")
	id, pending, err := w.Find(context.Background(), discussion, post)
	if id != 0 || pending || err != nil {
		t.Fatalf("id %d pending %v err %v", id, pending, err)
	}
	if c.slept < 30*time.Second || c.slept > 31*time.Second || f.Calls("getUpdates") > 40 {
		t.Fatalf("slept %v polls %d", c.slept, f.Calls("getUpdates"))
	}
}

func TestWaitingRetriesConflict(t *testing.T) {
	f := telegramtest.New(channel, discussion)
	w, c := waitingWith(f, 30*time.Second)
	post := send(t, f, "p")
	f.FailPolls(2, fmt.Errorf("telegram getUpdates: %w: terminated by other getUpdates request", telegram.ErrPollConflict))
	id, pending, err := w.Find(context.Background(), discussion, post)
	if err != nil || pending || id == 0 {
		t.Fatalf("id %d pending %v err %v", id, pending, err)
	}
	if c.sleeps != 2 {
		t.Fatalf("sleeps %d, want one pause per conflict", c.sleeps)
	}
}

func TestWaitingConflictUntilDeadline(t *testing.T) {
	f := telegramtest.New(channel, discussion)
	w, c := waitingWith(f, 30*time.Second)
	post := send(t, f, "p")
	f.FailPolls(1000, fmt.Errorf("telegram getUpdates: %w", telegram.ErrPollConflict))
	id, pending, err := w.Find(context.Background(), discussion, post)
	if id != 0 || pending || err != nil {
		t.Fatalf("id %d pending %v err %v", id, pending, err)
	}
	if c.slept < 30*time.Second || c.slept > 31*time.Second {
		t.Fatalf("slept %v, want the wait and no more", c.slept)
	}
}

func TestWaitingOtherPollErrorStops(t *testing.T) {
	f := telegramtest.New(channel, discussion)
	w, _ := waitingWith(f, 30*time.Second)
	post := send(t, f, "p")
	f.FailPolls(1, errors.New("telegram getUpdates: Unauthorized"))
	if _, _, err := w.Find(context.Background(), discussion, post); err == nil {
		t.Fatal("unauthorized poll was retried")
	}
}

func TestWaitingPollTimeout(t *testing.T) {
	w := Waiting(nil, time.Minute).(*waiting)
	for _, tc := range []struct{ remaining, want time.Duration }{
		{time.Minute, 5 * time.Second},
		{3500 * time.Millisecond, 3 * time.Second},
		{800 * time.Millisecond, 0},
	} {
		if got := w.pollTimeout(tc.remaining); got != tc.want {
			t.Fatalf("remaining %v: timeout %v, want %v", tc.remaining, got, tc.want)
		}
	}
}
