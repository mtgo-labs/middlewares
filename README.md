# middlewares

Invoker middlewares for [mtgo](https://github.com/mtgo-labs/mtgo) — register once and all outgoing Telegram RPC calls are automatically protected.

## Available middlewares

| Package | Description |
|---------|-------------|
| [floodwait](./floodwait) | Automatically retries RPC calls on `FLOOD_WAIT` errors |
| [metrics](./metrics) | Collects RPC-level metrics (requests, latency, flood waits, timeouts) |
| [ratelimit](./ratelimit) | Token-bucket rate limiter to prevent hitting Telegram API limits |
| [timeout](./timeout) | Applies context-based deadlines to RPC calls, preventing hangs |

## Quick start

Each middleware is a separate Go module. Install only what you need:

```bash
go get github.com/mtgo-labs/middlewares/floodwait
go get github.com/mtgo-labs/middlewares/metrics
go get github.com/mtgo-labs/middlewares/ratelimit
go get github.com/mtgo-labs/middlewares/timeout
```

Register with an mtgo client:

```go
import (
    "time"

    tg "github.com/mtgo-labs/mtgo/telegram"
    "github.com/mtgo-labs/middlewares/floodwait"
    "github.com/mtgo-labs/middlewares/metrics"
    "github.com/mtgo-labs/middlewares/ratelimit"
    "github.com/mtgo-labs/middlewares/timeout"
    "golang.org/x/time/rate"
)

client, _ := tg.NewClient(apiID, apiHash, &tg.Config{BotToken: botToken})

client.UseInvokerMiddleware(metrics.New(metrics.NewMemoryCollector(), metrics.Config{EnableMethodLabels: true}).Middleware())
client.UseInvokerMiddleware(floodwait.New().Middleware())
client.UseInvokerMiddleware(ratelimit.New(30, 10).Middleware())
client.UseInvokerMiddleware(timeout.Timeout(30 * time.Second))

client.Start()
```

Middlewares compose — register them in the order you want them applied.

## License

Apache License 2.0 ([LICENSE](./LICENSE))
