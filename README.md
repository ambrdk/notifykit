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

## Using the module

The repository is private, so Go has to skip the public proxy and checksum database for it.

Locally, set it for the command or your shell:

```bash
GOPRIVATE='github.com/ambrdk/*' go get github.com/ambrdk/notifykit@v0.1.0
```

In GitHub Actions, add a repo secret `NOTIFYKIT_TOKEN` with a fine-grained token that has read access to the contents of this repository, then before any step that downloads modules:

```yaml
env:
  GOPRIVATE: github.com/ambrdk/*
steps:
  - uses: actions/checkout@v5
  - run: git config --global url."https://x-access-token:${NOTIFYKIT_TOKEN}@github.com/ambrdk/".insteadOf "https://github.com/ambrdk/"
    env:
      NOTIFYKIT_TOKEN: ${{ secrets.NOTIFYKIT_TOKEN }}
```

In a Dockerfile, pass the token as a BuildKit secret so it stays out of the image layers:

```dockerfile
ENV GOPRIVATE=github.com/ambrdk/*
COPY go.mod go.sum ./
RUN --mount=type=secret,id=notifykit_token,required=true \
    GIT_CONFIG_COUNT=1 \
    GIT_CONFIG_KEY_0="url.https://x-access-token:$(cat /run/secrets/notifykit_token)@github.com/ambrdk/.insteadOf" \
    GIT_CONFIG_VALUE_0="https://github.com/ambrdk/" \
    go mod download
```

and build with `docker build --secret id=notifykit_token,env=NOTIFYKIT_TOKEN .` (or `secrets: notifykit_token=...` in `docker/build-push-action`).

## Versions

Tags are `v0.x.y`. Until `v1` a minor version may change the API.

## Development

```bash
go test -race ./...
```
