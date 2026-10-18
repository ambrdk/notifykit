# notifykit

Shared Go code for the services that post to chat channels. It has a Telegram Bot API client and a Prometheus adapter for it. The client goes through [go-telegram/bot](https://github.com/go-telegram/bot), but none of its types or errors leave the package.

## Packages

`telegram` sends, edits and reads messages and long-polls for updates. Callers react to two error outcomes:

- `ErrMessageGone`: the message being edited or replied to is no longer there (deleted, or Telegram refuses to edit it). Check with `errors.Is`.
- `*RateLimitedError`: Telegram answered 429. `RetryAfter` says how long to wait. The client does not retry by itself.

Editing a message with the same text it already has returns `nil`. Any other failure is an opaque error with the method name and Telegram's description in its text.

`telegram.WithObserver(fn)` gets called after every Bot API request with the method, the error (already in the terms above) and the duration. Polling reports only its errors, as method `getUpdates`.

`telegram/tgmetrics` turns those calls into a counter:

```go
reg := prometheus.NewRegistry()
client, err := telegram.New(token, "", log, telegram.WithObserver(tgmetrics.Observer(reg, "my_service")))
```

That registers `my_service_telegram_requests_total{method,result}` with `result` one of `ok`, `rate_limited`, `gone`, `error`. The `telegram` package itself does not import Prometheus, so a service without metrics does not pull it in.

`thread` keeps one channel post and one comment under it in the channel's discussion group. `Publish` takes the message ids from the last call (empty the first time) and the new texts, and returns the ids to store for next time. The first call sends the post, then waits for Telegram to copy it into the discussion group and replies to that copy. Later calls edit both messages. If someone deleted the post or the comment, it is sent again. If the copy does not show up within the wait, the post goes out without a comment and the next call tries again.

The comment needs the bot to read `getUpdates`, and Telegram serves updates for one token to one reader at a time. A bot that already long-polls somewhere else, like alert-relay, cannot also be used here. Give `thread` its own bot, or run its callers one at a time.

## Using the module

```bash
go get github.com/ambrdk/notifykit@v0.1.0
```

The module is public, so CI and Docker builds fetch it through the regular Go proxy with no tokens.

## Versions

Tags are `v0.x.y`. Until `v1` a minor version may change the API.

## Development

```bash
go test -race ./...
```
