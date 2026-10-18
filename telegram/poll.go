package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type pollRequest struct {
	Offset         int64    `json:"offset"`
	Timeout        int      `json:"timeout"`
	AllowedUpdates []string `json:"allowed_updates"`
}

type pollResponse struct {
	OK          bool            `json:"ok"`
	Result      []models.Update `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (c *Client) Poll(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	start := time.Now()
	updates, err := c.poll(ctx, offset, timeout)
	if err := c.finish("getUpdates", start, err); err != nil {
		return nil, err
	}
	out := make([]Update, 0, len(updates))
	for i := range updates {
		out = append(out, convert(&updates[i]))
	}
	return out, nil
}

func (c *Client) poll(ctx context.Context, offset int64, timeout time.Duration) ([]models.Update, error) {
	body, err := json.Marshal(pollRequest{Offset: offset, Timeout: int(timeout / time.Second), AllowedUpdates: []string{"message"}})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.server+"/bot"+c.token+"/getUpdates", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var r pollResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("decode getUpdates response: %w", err)
	}
	if r.OK {
		return r.Result, nil
	}
	switch r.ErrorCode {
	case http.StatusTooManyRequests:
		return nil, &bot.TooManyRequestsError{Message: r.Description, RetryAfter: r.Parameters.RetryAfter}
	case http.StatusBadRequest:
		return nil, fmt.Errorf("%w, %s", bot.ErrorBadRequest, r.Description)
	default:
		return nil, errors.New(r.Description)
	}
}
