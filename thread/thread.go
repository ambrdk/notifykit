package thread

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/ambrdk/notifykit/telegram"
)

const maxPoll = 5 * time.Second

type IDs struct {
	PostID    int64 `json:"post_id"`
	CopyID    int64 `json:"copy_id"`
	CommentID int64 `json:"comment_id"`
}

type Client interface {
	SendMessage(ctx context.Context, m telegram.Message) (int64, error)
	EditMessageText(ctx context.Context, chatID string, messageID int64, text string) error
	Poll(ctx context.Context, offset int64, timeout time.Duration) ([]telegram.Update, error)
}

type Publisher struct {
	c            Client
	channelID    string
	discussionID int64
	wait         time.Duration
}

func New(c Client, channelID string, discussionID int64, wait time.Duration) *Publisher {
	return &Publisher{c: c, channelID: channelID, discussionID: discussionID, wait: wait}
}

func (p *Publisher) Publish(ctx context.Context, ids IDs, post, comment string) (IDs, error) {
	if ids.PostID != 0 {
		err := p.c.EditMessageText(ctx, p.channelID, ids.PostID, post)
		switch {
		case errors.Is(err, telegram.ErrMessageGone):
			ids = IDs{}
		case err != nil:
			return ids, err
		}
	}
	if ids.PostID == 0 {
		id, err := p.c.SendMessage(ctx, telegram.Message{ChatID: p.channelID, Text: post, Silent: true})
		if err != nil {
			return ids, err
		}
		ids.PostID = id
	}
	if p.discussionID == 0 {
		return ids, nil
	}
	if ids.CopyID == 0 {
		copyID, err := p.waitCopy(ctx, ids.PostID)
		if err != nil || copyID == 0 {
			return ids, err
		}
		ids.CopyID = copyID
	}
	discussion := strconv.FormatInt(p.discussionID, 10)
	if ids.CommentID != 0 {
		err := p.c.EditMessageText(ctx, discussion, ids.CommentID, comment)
		switch {
		case err == nil:
			return ids, nil
		case errors.Is(err, telegram.ErrMessageGone):
			ids.CommentID = 0
		default:
			return ids, err
		}
	}
	id, err := p.c.SendMessage(ctx, telegram.Message{ChatID: discussion, Text: comment, Silent: true, ReplyTo: ids.CopyID})
	switch {
	case errors.Is(err, telegram.ErrMessageGone):
		ids.CopyID = 0
		return ids, nil
	case err != nil:
		return ids, err
	}
	ids.CommentID = id
	return ids, nil
}

func (p *Publisher) waitCopy(ctx context.Context, postID int64) (int64, error) {
	deadline := time.Now().Add(p.wait)
	var offset, copyID int64
	for copyID == 0 {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		updates, err := p.c.Poll(ctx, offset, min(remaining, maxPoll).Truncate(time.Second))
		if err != nil {
			return 0, err
		}
		for _, u := range updates {
			offset = max(offset, u.UpdateID+1)
			m := u.Message
			if copyID == 0 && m != nil && m.IsAutomaticForward && m.ChatID == p.discussionID && m.ForwardOriginMessageID == postID {
				copyID = m.MessageID
			}
		}
	}
	if offset > 0 {
		if _, err := p.c.Poll(ctx, offset, 0); err != nil {
			return copyID, err
		}
	}
	return copyID, nil
}
