package telegram

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
)

func badRequest(description string) error {
	return fmt.Errorf("%w, %s", bot.ErrorBadRequest, description)
}

func TestTranslate(t *testing.T) {
	isGone := func(err error) bool { return errors.Is(err, ErrMessageGone) }
	isOther := func(err error) bool {
		var rl *RateLimitedError
		return err != nil && !errors.Is(err, ErrMessageGone) && !errors.As(err, &rl)
	}
	cases := []struct {
		name string
		in   error
		want func(error) bool
	}{
		{"nil", nil, func(err error) bool { return err == nil }},
		{"not modified", badRequest("Bad Request: message is not modified: specified new message content and reply markup are exactly the same"), func(err error) bool { return err == nil }},
		{"not modified text without bad request", errors.New("message is not modified"), isOther},
		{"edit target gone", badRequest("Bad Request: message to edit not found"), isGone},
		{"edit refused", badRequest("Bad Request: message can't be edited"), isGone},
		{"reply target gone", badRequest("Bad Request: message to be replied not found"), isGone},
		{"chat not found", badRequest("Bad Request: chat not found"), isOther},
		{"empty text", badRequest("Bad Request: message text is empty"), isOther},
		{"rate limited", &bot.TooManyRequestsError{Message: "too many requests, Too Many Requests: retry after 7", RetryAfter: 7}, func(err error) bool {
			var rl *RateLimitedError
			return errors.As(err, &rl) && rl.RetryAfter == 7*time.Second
		}},
		{"forbidden", fmt.Errorf("%w, Forbidden: bot was kicked", bot.ErrorForbidden), func(err error) bool {
			return isOther(err) && !errors.Is(err, bot.ErrorForbidden) &&
				strings.Contains(err.Error(), "bot was kicked") && strings.Contains(err.Error(), "sendMessage")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := translate("sendMessage", tc.in)
			if !tc.want(got) {
				t.Fatalf("translate(%v) = %v", tc.in, got)
			}
			if got == nil {
				return
			}
			var tooMany *bot.TooManyRequestsError
			if errors.Is(got, bot.ErrorBadRequest) || errors.As(got, &tooMany) {
				t.Fatalf("bot error type leaked: %v", got)
			}
		})
	}
}
