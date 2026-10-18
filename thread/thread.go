package thread

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/ambrdk/notifykit/telegram"
)

const defaultMaxWait = 10 * time.Second

type IDs struct {
	PostID     int64 `json:"post_id"`
	CopyID     int64 `json:"copy_id"`
	CommentID  int64 `json:"comment_id"`
	CopyWaited bool  `json:"copy_waited,omitempty"`
}

type Status int

const (
	Commented Status = iota + 1
	Posted
	CopyPending
	NoCopy
	NoDiscussion
	Unthreaded
	PostGone
)

func (s Status) String() string {
	switch s {
	case Commented:
		return "commented"
	case Posted:
		return "posted"
	case CopyPending:
		return "copy pending"
	case NoCopy:
		return "no copy"
	case NoDiscussion:
		return "no discussion"
	case Unthreaded:
		return "unthreaded"
	case PostGone:
		return "post gone"
	}
	return "unknown"
}

type Post struct {
	Text    string
	Comment func(skip int) string
	Loud    bool
	Final   bool
}

type Outcome struct {
	IDs    IDs
	Status Status
}

type Client interface {
	SendMessage(ctx context.Context, m telegram.Message) (int64, error)
	EditMessageText(ctx context.Context, chatID string, messageID int64, text string) error
	GetChat(ctx context.Context, chatID string) (telegram.Chat, error)
}

type Copies interface {
	Find(ctx context.Context, discussionID, postID int64) (copyID int64, pending bool, err error)
}

type Option func(*Thread)

func WithMaxWait(d time.Duration) Option {
	return func(t *Thread) { t.maxWait = d }
}

type Thread struct {
	c          Client
	channelID  string
	copies     Copies
	maxWait    time.Duration
	mu         sync.Mutex
	discussion int64
}

func New(c Client, channelID string, copies Copies, opts ...Option) *Thread {
	t := &Thread{c: c, channelID: channelID, copies: copies, maxWait: defaultMaxWait}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *Thread) Publish(ctx context.Context, ids IDs, p Post) (Outcome, error) {
	text := fit(p.Text)
	if ids.PostID != 0 {
		err := t.call(ctx, func() error { return t.c.EditMessageText(ctx, t.channelID, ids.PostID, text) })
		switch {
		case errors.Is(err, telegram.ErrMessageGone) && p.Final:
			return Outcome{Status: PostGone}, nil
		case errors.Is(err, telegram.ErrMessageGone):
			ids = IDs{}
		case err != nil:
			return Outcome{IDs: ids}, err
		}
	}
	if ids.PostID == 0 {
		id, err := t.send(ctx, telegram.Message{ChatID: t.channelID, Text: text, Silent: !p.Loud})
		if err != nil {
			return Outcome{IDs: ids}, err
		}
		ids.PostID = id
	}
	if p.Comment == nil {
		return Outcome{IDs: ids, Status: Posted}, nil
	}
	discussion := t.discussionID(ctx)
	if discussion == 0 {
		return Outcome{IDs: ids, Status: NoDiscussion}, nil
	}
	if ids.CopyID == 0 && ids.CommentID == 0 {
		var copyID int64
		var pending bool
		err := t.call(ctx, func() error {
			var err error
			copyID, pending, err = t.copies.Find(ctx, discussion, ids.PostID)
			return err
		})
		switch {
		case err != nil:
			return Outcome{IDs: ids}, err
		case copyID != 0:
			ids.CopyID = copyID
		case !ids.CopyWaited:
			ids.CopyWaited = true
			if pending {
				return Outcome{IDs: ids, Status: CopyPending}, nil
			}
			return Outcome{IDs: ids, Status: NoCopy}, nil
		}
	}
	return t.comment(ctx, ids, strconv.FormatInt(discussion, 10), render(p.Comment))
}

func (t *Thread) comment(ctx context.Context, ids IDs, chat, body string) (Outcome, error) {
	if ids.CommentID != 0 {
		err := t.call(ctx, func() error { return t.c.EditMessageText(ctx, chat, ids.CommentID, body) })
		switch {
		case err == nil:
			return Outcome{IDs: ids, Status: threaded(ids)}, nil
		case errors.Is(err, telegram.ErrMessageGone):
			ids.CommentID = 0
		default:
			return Outcome{IDs: ids}, err
		}
	}
	id, err := t.send(ctx, telegram.Message{ChatID: chat, Text: body, Silent: true, ReplyTo: ids.CopyID})
	if errors.Is(err, telegram.ErrMessageGone) && ids.CopyID != 0 {
		ids.CopyID = 0
		id, err = t.send(ctx, telegram.Message{ChatID: chat, Text: body, Silent: true})
	}
	if err != nil {
		return Outcome{IDs: ids}, err
	}
	ids.CommentID = id
	return Outcome{IDs: ids, Status: threaded(ids)}, nil
}

func threaded(ids IDs) Status {
	if ids.CopyID != 0 {
		return Commented
	}
	return Unthreaded
}

func (t *Thread) send(ctx context.Context, m telegram.Message) (int64, error) {
	var id int64
	err := t.call(ctx, func() error {
		var err error
		id, err = t.c.SendMessage(ctx, m)
		return err
	})
	return id, err
}

func (t *Thread) discussionID(ctx context.Context) int64 {
	t.mu.Lock()
	known := t.discussion
	t.mu.Unlock()
	if known != 0 {
		return known
	}
	var chat telegram.Chat
	err := t.call(ctx, func() error {
		var err error
		chat, err = t.c.GetChat(ctx, t.channelID)
		return err
	})
	if err != nil || chat.LinkedChatID == 0 {
		return 0
	}
	t.mu.Lock()
	t.discussion = chat.LinkedChatID
	t.mu.Unlock()
	return chat.LinkedChatID
}

func (t *Thread) call(ctx context.Context, fn func() error) error {
	err := fn()
	var limited *telegram.RateLimitedError
	if !errors.As(err, &limited) || limited.RetryAfter > t.maxWait {
		return err
	}
	timer := time.NewTimer(limited.RetryAfter)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	return fn()
}
