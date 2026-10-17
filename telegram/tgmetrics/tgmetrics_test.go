package tgmetrics

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ambrdk/notifykit/telegram"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestObserverCountsResults(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := Observer(reg, "test")
	obs(telegram.Call{Method: "sendMessage"})
	obs(telegram.Call{Method: "sendMessage", Err: &telegram.RateLimitedError{RetryAfter: time.Second}})
	obs(telegram.Call{Method: "editMessageText", Err: fmt.Errorf("x: %w", telegram.ErrMessageGone)})
	obs(telegram.Call{Method: "getUpdates", Err: errors.New("down")})
	want := `# HELP test_telegram_requests_total Telegram Bot API calls by method and result.
# TYPE test_telegram_requests_total counter
test_telegram_requests_total{method="editMessageText",result="gone"} 1
test_telegram_requests_total{method="getUpdates",result="error"} 1
test_telegram_requests_total{method="sendMessage",result="ok"} 1
test_telegram_requests_total{method="sendMessage",result="rate_limited"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "test_telegram_requests_total"); err != nil {
		t.Fatal(err)
	}
}

func TestObserverConcurrent(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := Observer(reg, "test")
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				obs(telegram.Call{Method: "sendMessage"})
			}
		}()
	}
	wg.Wait()
	got, err := testutil.GatherAndCount(reg, "test_telegram_requests_total")
	if err != nil || got != 1 {
		t.Fatalf("series %d err %v", got, err)
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if v := mfs[0].GetMetric()[0].GetCounter().GetValue(); v != 800 {
		t.Fatalf("count %v", v)
	}
}
