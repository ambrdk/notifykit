package thread

import (
	"context"
	"sync"
	"time"

	"github.com/ambrdk/notifykit/telegram"
)

type copyKey struct {
	chatID int64
	postID int64
}

type seenCopy struct {
	id   int64
	seen time.Time
}

type Feed struct {
	mu     sync.Mutex
	ttl    time.Duration
	now    func() time.Time
	copies map[copyKey]seenCopy
	onCopy func(postID int64)
}

func NewFeed(ttl time.Duration, now func() time.Time) *Feed {
	return &Feed{ttl: ttl, now: now, copies: map[copyKey]seenCopy{}}
}

func (f *Feed) OnCopy(fn func(postID int64)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onCopy = fn
}

func (f *Feed) Observe(u telegram.Update) {
	m := u.Message
	if m == nil || !m.IsAutomaticForward || m.ForwardOriginMessageID == 0 {
		return
	}
	f.mu.Lock()
	f.expire()
	f.copies[copyKey{chatID: m.ChatID, postID: m.ForwardOriginMessageID}] = seenCopy{id: m.MessageID, seen: f.now()}
	fn := f.onCopy
	f.mu.Unlock()
	if fn != nil {
		fn(m.ForwardOriginMessageID)
	}
}

func (f *Feed) Find(_ context.Context, discussionID, postID int64) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expire()
	key := copyKey{chatID: discussionID, postID: postID}
	c, ok := f.copies[key]
	if !ok {
		return 0, true, nil
	}
	delete(f.copies, key)
	return c.id, false, nil
}

func (f *Feed) expire() {
	now := f.now()
	for k, c := range f.copies {
		if now.Sub(c.seen) > f.ttl {
			delete(f.copies, k)
		}
	}
}
