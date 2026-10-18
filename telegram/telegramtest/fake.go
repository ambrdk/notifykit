package telegramtest

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ambrdk/notifykit/telegram"
)

const (
	maxText  = 4096
	pageSize = 100
)

type Sent struct {
	ID      int64
	Text    string
	ReplyTo int64
	Silent  bool
}

type Fake struct {
	mu         sync.Mutex
	channel    string
	discussion int64
	chats      map[string][]*Sent
	deleted    map[string]map[int64]bool
	queue      []telegram.Update
	lastUpdate int64
	hold       bool
	held       map[int64]telegram.Update
	listeners  []func(telegram.Update)
	limitCalls int
	limitAfter time.Duration
	getChatErr error
	calls      map[string]int
	now        func() time.Time
}

func New(channelID string, discussionID int64) *Fake {
	return &Fake{
		channel:    channelID,
		discussion: discussionID,
		chats:      map[string][]*Sent{},
		deleted:    map[string]map[int64]bool{},
		held:       map[int64]telegram.Update{},
		calls:      map[string]int{},
		now:        time.Now,
	}
}

func (f *Fake) SendMessage(_ context.Context, m telegram.Message) (int64, error) {
	f.mu.Lock()
	if err := f.begin("sendMessage"); err != nil {
		f.mu.Unlock()
		return 0, err
	}
	if utf8.RuneCountInString(m.Text) > maxText {
		f.mu.Unlock()
		return 0, errors.New("telegram sendMessage: Bad Request: message is too long")
	}
	if m.ReplyTo != 0 && f.find(m.ChatID, m.ReplyTo) == nil {
		f.mu.Unlock()
		return 0, fmt.Errorf("telegram sendMessage: %w: Bad Request: message to be replied not found", telegram.ErrMessageGone)
	}
	id := f.add(m.ChatID, Sent{Text: m.Text, ReplyTo: m.ReplyTo, Silent: m.Silent})
	var deliver []telegram.Update
	if m.ChatID == f.channel && f.discussion != 0 {
		group := strconv.FormatInt(f.discussion, 10)
		copyID := f.add(group, Sent{Text: m.Text})
		f.lastUpdate++
		u := telegram.Update{UpdateID: f.lastUpdate, Message: &telegram.ChatMessage{
			MessageID:              copyID,
			ChatID:                 f.discussion,
			IsAutomaticForward:     true,
			ForwardOriginMessageID: id,
			Date:                   f.now(),
		}}
		if f.hold {
			f.held[id] = u
		} else {
			f.queue = append(f.queue, u)
			deliver = append(deliver, u)
		}
	}
	listeners := append([]func(telegram.Update){}, f.listeners...)
	f.mu.Unlock()
	notify(listeners, deliver)
	return id, nil
}

func (f *Fake) EditMessageText(_ context.Context, chatID string, messageID int64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("editMessageText"); err != nil {
		return err
	}
	if utf8.RuneCountInString(text) > maxText {
		return errors.New("telegram editMessageText: Bad Request: message is too long")
	}
	m := f.find(chatID, messageID)
	if m == nil {
		return fmt.Errorf("telegram editMessageText: %w: Bad Request: message to edit not found", telegram.ErrMessageGone)
	}
	m.Text = text
	return nil
}

func (f *Fake) GetChat(_ context.Context, chatID string) (telegram.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("getChat"); err != nil {
		return telegram.Chat{}, err
	}
	if f.getChatErr != nil {
		err := f.getChatErr
		f.getChatErr = nil
		return telegram.Chat{}, err
	}
	id, _ := strconv.ParseInt(chatID, 10, 64)
	chat := telegram.Chat{ID: id}
	if chatID == f.channel {
		chat.LinkedChatID = f.discussion
	}
	return chat, nil
}

func (f *Fake) Poll(_ context.Context, offset int64, _ time.Duration) ([]telegram.Update, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("getUpdates"); err != nil {
		return nil, err
	}
	if offset > 0 {
		kept := f.queue[:0]
		for _, u := range f.queue {
			if u.UpdateID >= offset {
				kept = append(kept, u)
			}
		}
		f.queue = kept
	}
	n := min(len(f.queue), pageSize)
	return append([]telegram.Update(nil), f.queue[:n]...), nil
}

func (f *Fake) HoldCopies(hold bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hold = hold
}

func (f *Fake) ReleaseCopy(postID int64) {
	f.mu.Lock()
	u, ok := f.held[postID]
	if ok {
		delete(f.held, postID)
		f.queue = append(f.queue, u)
	}
	listeners := append([]func(telegram.Update){}, f.listeners...)
	f.mu.Unlock()
	if ok {
		notify(listeners, []telegram.Update{u})
	}
}

func (f *Fake) Delete(chatID string, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleted[chatID] == nil {
		f.deleted[chatID] = map[int64]bool{}
	}
	f.deleted[chatID][id] = true
}

func (f *Fake) RateLimit(calls int, after time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.limitCalls, f.limitAfter = calls, after
}

func (f *Fake) FailGetChat(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getChatErr = err
}

func (f *Fake) OnUpdate(fn func(telegram.Update)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listeners = append(f.listeners, fn)
}

func (f *Fake) Enqueue(u telegram.Update) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, u)
	f.lastUpdate = max(f.lastUpdate, u.UpdateID)
}

func (f *Fake) Messages(chatID string) []Sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Sent
	for _, m := range f.chats[chatID] {
		if !f.deleted[chatID][m.ID] {
			out = append(out, *m)
		}
	}
	return out
}

func (f *Fake) Calls(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func (f *Fake) begin(method string) error {
	f.calls[method]++
	if f.limitCalls > 0 {
		f.limitCalls--
		return &telegram.RateLimitedError{RetryAfter: f.limitAfter}
	}
	return nil
}

func (f *Fake) add(chatID string, s Sent) int64 {
	s.ID = int64(len(f.chats[chatID]) + 1)
	f.chats[chatID] = append(f.chats[chatID], &s)
	return s.ID
}

func (f *Fake) find(chatID string, id int64) *Sent {
	if f.deleted[chatID][id] {
		return nil
	}
	for _, m := range f.chats[chatID] {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func notify(listeners []func(telegram.Update), updates []telegram.Update) {
	for _, u := range updates {
		for _, fn := range listeners {
			fn(u)
		}
	}
}
