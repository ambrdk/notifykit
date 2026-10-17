package telegram

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const pollTimeout = time.Minute

type Client struct {
	b        *bot.Bot
	mu       sync.Mutex
	onUpdate func(Update)
}

type Message struct {
	ChatID  string
	Text    string
	Silent  bool
	ReplyTo int64
}

type Chat struct {
	ID           int64
	LinkedChatID int64
}

type Update struct {
	UpdateID int64
	Message  *ChatMessage
}

type ChatMessage struct {
	MessageID              int64
	ChatID                 int64
	IsAutomaticForward     bool
	ForwardOriginMessageID int64
}

func New(token, serverURL string, log *slog.Logger) (*Client, error) {
	c := &Client{}
	opts := []bot.Option{
		bot.WithSkipGetMe(),
		bot.WithHTTPClient(pollTimeout, &http.Client{Timeout: pollTimeout + 10*time.Second}),
		bot.WithAllowedUpdates(bot.AllowedUpdates{"message"}),
		bot.WithNotAsyncHandlers(),
		bot.WithDefaultHandler(func(_ context.Context, _ *bot.Bot, u *models.Update) { c.dispatch(u) }),
		bot.WithErrorsHandler(func(err error) { log.Warn("telegram polling error", "error", err) }),
	}
	if serverURL != "" {
		opts = append(opts, bot.WithServerURL(serverURL))
	}
	b, err := bot.New(token, opts...)
	if err != nil {
		return nil, err
	}
	c.b = b
	return c, nil
}

func (c *Client) SendMessage(ctx context.Context, m Message) (int64, error) {
	params := &bot.SendMessageParams{
		ChatID:              m.ChatID,
		Text:                m.Text,
		ParseMode:           models.ParseModeHTML,
		DisableNotification: m.Silent,
		LinkPreviewOptions:  noPreview(),
	}
	if m.ReplyTo != 0 {
		params.ReplyParameters = &models.ReplyParameters{MessageID: int(m.ReplyTo)}
	}
	msg, err := c.b.SendMessage(ctx, params)
	if err != nil {
		return 0, translate("sendMessage", err)
	}
	return int64(msg.ID), nil
}

func (c *Client) EditMessageText(ctx context.Context, chatID string, messageID int64, text string) error {
	_, err := c.b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:             chatID,
		MessageID:          int(messageID),
		Text:               text,
		ParseMode:          models.ParseModeHTML,
		LinkPreviewOptions: noPreview(),
	})
	return translate("editMessageText", err)
}

func noPreview() *models.LinkPreviewOptions {
	disabled := true
	return &models.LinkPreviewOptions{IsDisabled: &disabled}
}

func (c *Client) GetChat(ctx context.Context, chatID string) (Chat, error) {
	chat, err := c.b.GetChat(ctx, &bot.GetChatParams{ChatID: chatID})
	if err != nil {
		return Chat{}, translate("getChat", err)
	}
	return Chat{ID: chat.ID, LinkedChatID: chat.LinkedChatID}, nil
}

func (c *Client) Listen(ctx context.Context, onUpdate func(Update)) {
	c.mu.Lock()
	c.onUpdate = onUpdate
	c.mu.Unlock()
	c.b.Start(ctx)
}

func (c *Client) dispatch(u *models.Update) {
	c.mu.Lock()
	fn := c.onUpdate
	c.mu.Unlock()
	if fn == nil || u == nil {
		return
	}
	fn(convert(u))
}

func convert(u *models.Update) Update {
	out := Update{UpdateID: u.ID}
	m := u.Message
	if m == nil {
		return out
	}
	cm := &ChatMessage{MessageID: int64(m.ID), ChatID: m.Chat.ID, IsAutomaticForward: m.IsAutomaticForward}
	if m.ForwardOrigin != nil && m.ForwardOrigin.MessageOriginChannel != nil {
		cm.ForwardOriginMessageID = int64(m.ForwardOrigin.MessageOriginChannel.MessageID)
	}
	out.Message = cm
	return out
}
