package tgmetrics

import (
	"errors"

	"github.com/ambrdk/notifykit/telegram"
	"github.com/prometheus/client_golang/prometheus"
)

func Observer(reg prometheus.Registerer, namespace string) func(telegram.Call) {
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: namespace + "_telegram_requests_total",
		Help: "Telegram Bot API calls by method and result.",
	}, []string{"method", "result"})
	reg.MustRegister(requests)
	return func(c telegram.Call) {
		requests.WithLabelValues(c.Method, result(c.Err)).Inc()
	}
}

func result(err error) string {
	var limited *telegram.RateLimitedError
	switch {
	case err == nil:
		return "ok"
	case errors.As(err, &limited):
		return "rate_limited"
	case errors.Is(err, telegram.ErrMessageGone):
		return "gone"
	default:
		return "error"
	}
}
