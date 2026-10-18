package thread

import (
	"context"
	"time"

	"github.com/ambrdk/notifykit/telegram"
)

const (
	maxPoll   = 5 * time.Second
	pollPause = time.Second
	ackAge    = time.Hour
)

type Poller interface {
	Poll(ctx context.Context, offset int64, timeout time.Duration) ([]telegram.Update, error)
}

type waiting struct {
	p     Poller
	wait  time.Duration
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

func Waiting(p Poller, wait time.Duration) Copies {
	return &waiting{p: p, wait: wait, now: time.Now, sleep: sleepCtx}
}

func (w *waiting) Find(ctx context.Context, discussionID, postID int64) (int64, bool, error) {
	deadline := w.now().Add(w.wait)
	for {
		updates, err := w.p.Poll(ctx, 0, w.pollTimeout(deadline.Sub(w.now())))
		if err != nil {
			return 0, false, err
		}
		copyID := match(updates, discussionID, postID)
		if err := w.ackOld(ctx, updates); err != nil {
			return copyID, false, err
		}
		remaining := deadline.Sub(w.now())
		if copyID != 0 || remaining <= 0 {
			return copyID, false, nil
		}
		if err := w.sleep(ctx, min(pollPause, remaining)); err != nil {
			return 0, false, err
		}
	}
}

func (w *waiting) pollTimeout(remaining time.Duration) time.Duration {
	return min(remaining, maxPoll).Truncate(time.Second)
}

func (w *waiting) ackOld(ctx context.Context, updates []telegram.Update) error {
	var last int64
	cutoff := w.now().Add(-ackAge)
	for _, u := range updates {
		if u.Message != nil && !u.Message.Date.Before(cutoff) {
			break
		}
		last = u.UpdateID
	}
	if last == 0 {
		return nil
	}
	_, err := w.p.Poll(ctx, last+1, 0)
	return err
}

func match(updates []telegram.Update, discussionID, postID int64) int64 {
	for _, u := range updates {
		if m := u.Message; m != nil && m.IsAutomaticForward && m.ChatID == discussionID && m.ForwardOriginMessageID == postID {
			return m.MessageID
		}
	}
	return 0
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
