package thread

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ambrdk/notifykit/telegram"
	"github.com/ambrdk/notifykit/telegram/telegramtest"
)

func copyUpdate(chatID, copyID, postID int64) telegram.Update {
	return telegram.Update{Message: &telegram.ChatMessage{MessageID: copyID, ChatID: chatID, IsAutomaticForward: true, ForwardOriginMessageID: postID}}
}

func TestFeedCopyBeforeFind(t *testing.T) {
	feed := NewFeed(5*time.Minute, time.Now)
	feed.Observe(copyUpdate(discussion, 7, 3))
	id, pending, err := feed.Find(context.Background(), discussion, 3)
	if id != 7 || pending || err != nil {
		t.Fatalf("id %d pending %v err %v", id, pending, err)
	}
	if id, pending, _ := feed.Find(context.Background(), discussion, 3); id != 0 || !pending {
		t.Fatalf("claimed copy found again: %d %v", id, pending)
	}
}

func TestFeedCopyAfterFind(t *testing.T) {
	feed := NewFeed(5*time.Minute, time.Now)
	var arrived []int64
	feed.OnCopy(func(postID int64) { arrived = append(arrived, postID) })
	if id, pending, _ := feed.Find(context.Background(), discussion, 3); id != 0 || !pending {
		t.Fatalf("id %d pending %v", id, pending)
	}
	feed.Observe(copyUpdate(discussion, 7, 3))
	if len(arrived) != 1 || arrived[0] != 3 {
		t.Fatalf("arrived %v", arrived)
	}
	if id, _, _ := feed.Find(context.Background(), discussion, 3); id != 7 {
		t.Fatalf("id %d", id)
	}
}

func TestFeedExpires(t *testing.T) {
	now := time.Now()
	feed := NewFeed(5*time.Minute, func() time.Time { return now })
	feed.Observe(copyUpdate(discussion, 7, 3))
	now = now.Add(6 * time.Minute)
	if id, _, _ := feed.Find(context.Background(), discussion, 3); id != 0 {
		t.Fatalf("expired copy found: %d", id)
	}
}

func TestFeedIgnoresOtherChats(t *testing.T) {
	feed := NewFeed(5*time.Minute, time.Now)
	called := false
	feed.OnCopy(func(int64) { called = true })
	feed.Observe(copyUpdate(-9, 7, 3))
	feed.Observe(telegram.Update{Message: &telegram.ChatMessage{MessageID: 8, ChatID: discussion, ForwardOriginMessageID: 3}})
	feed.Observe(telegram.Update{})
	if id, _, _ := feed.Find(context.Background(), discussion, 3); id != 0 {
		t.Fatalf("found %d", id)
	}
	if id, _, _ := feed.Find(context.Background(), -9, 3); id != 7 {
		t.Fatalf("other chat copy %d", id)
	}
	if !called {
		t.Fatal("copy in the other chat not reported")
	}
}

func TestFeedCallbackOutsideLock(t *testing.T) {
	feed := NewFeed(5*time.Minute, time.Now)
	var got int64
	feed.OnCopy(func(postID int64) { got, _, _ = feed.Find(context.Background(), discussion, postID) })
	done := make(chan struct{})
	go func() {
		feed.Observe(copyUpdate(discussion, 7, 3))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock")
	}
	if got != 7 {
		t.Fatalf("got %d", got)
	}
}

func TestThreadWithFeed(t *testing.T) {
	f := telegramtest.New(channel, discussion)
	feed := NewFeed(5*time.Minute, time.Now)
	f.OnUpdate(feed.Observe)
	th := New(f, channel, feed)
	ctx := context.Background()
	var mu sync.Mutex
	var ids IDs
	var final Outcome
	feed.OnCopy(func(int64) {
		mu.Lock()
		defer mu.Unlock()
		out, err := th.Publish(ctx, ids, post("p", "c"))
		if err != nil {
			t.Error(err)
		}
		ids, final = out.IDs, out
	})
	f.HoldCopies(true)
	mu.Lock()
	first, err := th.Publish(ctx, IDs{}, post("p", "c"))
	ids = first.IDs
	mu.Unlock()
	if err != nil || first.Status != CopyPending {
		t.Fatalf("first %+v err %v", first, err)
	}
	f.ReleaseCopy(first.IDs.PostID)
	mu.Lock()
	defer mu.Unlock()
	if final.Status != Commented || f.Messages(group)[1].ReplyTo != final.IDs.CopyID {
		t.Fatalf("final %+v discussion %+v", final, f.Messages(group))
	}
}
