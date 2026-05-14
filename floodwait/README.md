# mtgo middleware: floodwait

Invoker middleware that automatically handles Telegram FLOOD_WAIT errors by sleeping for the required duration and retrying the RPC call.

## Install

```bash
go get github.com/mtgo-labs/middlewares/floodwait
```

## Usage

```go
import (
    "log"
    "time"
    tg "github.com/mtgo-labs/mtgo/telegram"
    "github.com/mtgo-labs/middlewares/floodwait"
)

func main() {
    client, _ := tg.NewClient(apiID, apiHash, &tg.Config{BotToken: botToken})

    waiter := floodwait.New()
    waiter.OnWait(func(d time.Duration) {
        log.Printf("flood wait: %v", d)
    })
    waiter.WithMaxWait(60 * time.Second)
    waiter.WithMaxRetries(5)

    client.UseInvokerMiddleware(waiter.Middleware())

    // That's it. All RPC calls are automatically retried on FLOOD_WAIT.
    // No handler changes needed.
    client.Start()
}
```

## How it works

1. RPC call returns FLOOD_WAIT error
2. Middleware extracts the wait duration
3. Sleeps for `duration + 1s` buffer (respecting context cancellation)
4. Retries the RPC call
5. Repeats up to `MaxRetries` (default: 5)

No handler changes required — register once and all outgoing API calls (`ctx.Reply`, `ctx.SendMessage`, `client.Invoke`, etc.) are automatically protected.

## License

Apache License 2.0
