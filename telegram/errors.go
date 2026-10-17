package telegram

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"
)

var ErrMessageGone = errors.New("telegram: message is gone")

type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return "telegram: rate limited, retry after " + e.RetryAfter.String()
}

var goneDescriptions = []string{
	"message to edit not found",
	"message can't be edited",
	"message to be replied not found",
}

func isNotModified(err error) bool {
	return errors.Is(err, bot.ErrorBadRequest) && strings.Contains(err.Error(), "message is not modified")
}

func translate(method string, err error) error {
	if err == nil || isNotModified(err) {
		return nil
	}
	var tooMany *bot.TooManyRequestsError
	if errors.As(err, &tooMany) {
		return &RateLimitedError{RetryAfter: time.Duration(tooMany.RetryAfter) * time.Second}
	}
	if errors.Is(err, bot.ErrorBadRequest) {
		for _, d := range goneDescriptions {
			if strings.Contains(err.Error(), d) {
				return fmt.Errorf("telegram %s: %w: %s", method, ErrMessageGone, err.Error())
			}
		}
	}
	return fmt.Errorf("telegram %s: %s", method, err.Error())
}
