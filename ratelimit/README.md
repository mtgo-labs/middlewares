# mtgo middleware: ratelimit

Invoker middleware that throttles outgoing Telegram RPC calls using a token-bucket rate limiter.

## Install

```bash
go get github.com/mtgo-labs/middlewares/ratelimit
```

## Usage

```go
import (
    "time"
    tg "github.com/mtgo-labs/mtgo/telegram"
    "github.com/mtgo-labs/middlewares/ratelimit"
    "golang.org/x/time/rate"
)

func main() {
    client, _ := tg.NewClient(apiID, apiHash, &tg.Config{BotToken: botToken})

    // Allow at most 30 RPC calls per second, burst of 10.
    mw := ratelimit.New(30, 10)
    client.UseInvokerMiddleware(mw.Middleware())

    // Or: 1 call per 100ms (10/sec), burst of 1.
    mw := ratelimit.New(rate.Every(100*time.Millisecond), 1)
    client.UseInvokerMiddleware(mw.Middleware())

    client.Start()
}
```

## How it works

Before each RPC call to Telegram, the middleware acquires a token from a `golang.org/x/time/rate.Limiter`. If no tokens are available, it blocks until one becomes free (respecting context cancellation). No handler changes required — just register and all outgoing API calls are rate-limited.

## License

Apache License 2.0
