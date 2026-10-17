package telegram

import "time"

type Call struct {
	Method   string
	Err      error
	Duration time.Duration
}

type options struct {
	observe func(Call)
}

type Option func(*options)

func WithObserver(fn func(Call)) Option {
	return func(o *options) { o.observe = fn }
}

func (c *Client) finish(method string, start time.Time, err error) error {
	err = translate(method, err)
	if c.observe != nil {
		c.observe(Call{Method: method, Err: err, Duration: time.Since(start)})
	}
	return err
}

func (c *Client) pollingError(err error) {
	if c.observe != nil {
		c.observe(Call{Method: "getUpdates", Err: translate("getUpdates", err)})
	}
}
