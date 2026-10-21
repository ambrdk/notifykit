# notifykit

Shared Go code for the services that post to chat channels. It has a Telegram Bot API client and a Prometheus adapter for it. The client goes through [go-telegram/bot](https://github.com/go-telegram/bot), but none of its types or errors leave the package.

## Packages

`telegram` sends, edits and reads messages and long-polls for updates. Callers react to three error outcomes:

- `ErrMessageGone`: the message being edited or replied to is no longer there (deleted, or Telegram refuses to edit it). Check with `errors.Is`.
- `*RateLimitedError`: Telegram answered 429. `RetryAfter` says how long to wait. The client does not retry by itself.
- `ErrPollConflict`: Telegram answered 409 to `getUpdates` because another reader of the same token holds the poll. Check with `errors.Is`.

Editing a message with the same text it already has returns `nil`. Any other failure is an opaque error with the method name and Telegram's description in its text.

`telegram.WithObserver(fn)` gets called after every Bot API request with the method, the error (already in the terms above) and the duration. Polling reports only its errors, as method `getUpdates`.

`telegram/tgmetrics` turns those calls into a counter:

```go
reg := prometheus.NewRegistry()
client, err := telegram.New(token, "", log, telegram.WithObserver(tgmetrics.Observer(reg, "my_service")))
```

That registers `my_service_telegram_requests_total{method,result}` with `result` one of `ok`, `rate_limited`, `gone`, `error`. The `telegram` package itself does not import Prometheus, so a service without metrics does not pull it in.

`thread` keeps one channel post and one comment under it in the channel's discussion group. Telegram copies every channel post into that group, and a comment is a reply to the copy.

```go
th := thread.New(client, channelID, copies)
out, err := th.Publish(ctx, ids, thread.Post{Text: post, Comment: render})
```

`Publish` takes the ids from the last call (empty the first time) and returns the ids to store for next time, even when it also returns an error. `out.Status` says what happened to the comment:

| Status | Meaning |
|---|---|
| `Commented` | the comment is a reply to the post's copy |
| `Posted` | `Post.Comment` was nil, only the post was sent |
| `CopyPending` | the copy has not arrived yet, a later call will comment |
| `NoCopy` | the copy did not arrive within the wait |
| `NoDiscussion` | the channel has no discussion group, or reading it failed |
| `Unthreaded` | the copy never arrived or is gone, so the comment went to the group without a reply |
| `PostGone` | `Post.Final` was set and the post had been deleted, nothing was sent |

The module handles the cases that used to live in every caller:

- It looks up the discussion group itself and retries on the next call if that fails.
- A deleted post is sent again, unless `Post.Final` is set. A deleted comment is sent again.
- A 429 that asks to wait no longer than `WithMaxWait` (10 s by default) is retried once. A longer one comes back as `*telegram.RateLimitedError`.
- Text over 4096 characters is shortened. `Post.Comment` gets called with a growing `skip` so the caller can drop old lines and keep its markup intact. Only if that does not help is the text cut as plain text.
- The post is silent unless `Post.Loud` is set. Comments are always silent.

`Copies` is how the copy reaches the thread. There are two adapters:

- `thread.Waiting(client, wait)` is for a short-lived process like a CI step. It long-polls `getUpdates` for up to `wait` and does not acknowledge other posts' updates, so a copy that arrives late is still there for the next run. Only updates older than an hour are acknowledged. Telegram serves updates for one token to one reader at a time. When another reader holds the poll, `Find` pauses for a second and asks again until `wait` runs out, so callers of one bot may overlap; still give this bot to no one else.
- `thread.NewFeed(ttl, now)` is for a long-running service that already listens: pass `feed.Observe` to `client.Listen` and `feed` to `thread.New`. `Publish` does not block. `feed.OnCopy(fn)` reports the post id when its copy arrives, and the caller calls `Publish` again to post the comment.

`telegram/telegramtest` is a fake client for tests: sends, edits, deleted messages, automatic copies into the discussion group (which can be held back), the `getUpdates` queue and rate limits behave as in Telegram. `FailPolls(n, err)` makes the next `n` polls fail.

## Using the module

```bash
go get github.com/ambrdk/notifykit@v0.3.0
```

The module is public, so CI and Docker builds fetch it through the regular Go proxy with no tokens.

## Versions

Tags are `v0.x.y`. Until `v1` a minor version may change the API.

## Development

```bash
go test -race ./...
```
