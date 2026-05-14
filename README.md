# middlewares

Invoker middlewares for [mtgo](https://github.com/mtgo-labs/mtgo) — register once and all outgoing Telegram RPC calls are automatically protected.

## Available middlewares

| Package | Description |
|---------|-------------|
| [floodwait](./floodwait) | Automatically retries RPC calls on `FLOOD_WAIT` errors |
| [ratelimit](./ratelimit) | Token-bucket rate limiter to prevent hitting Telegram API limits |

## Quick start

Each middleware is a separate Go module. Install only what you need:

```bash
go get github.com/mtgo-labs/middlewares/floodwait
go get github.com/mtgo-labs/middlewares/ratelimit
```

Register with an mtgo client:

```go
import (
    tg "github.com/mtgo-labs/mtgo/telegram"
    "github.com/mtgo-labs/middlewares/floodwait"
    "github.com/mtgo-labs/middlewares/ratelimit"
    "golang.org/x/time/rate"
)

client, _ := tg.NewClient(apiID, apiHash, &tg.Config{BotToken: botToken})

client.UseInvokerMiddleware(floodwait.New().Middleware())
client.UseInvokerMiddleware(ratelimit.New(30, 10).Middleware())

client.Start()
```

Middlewares compose — register them in the order you want them applied.

## License

Apache License 2.0 ([LICENSE](./LICENSE))
