# timeout

Invoker middleware for [mtgo](https://github.com/mtgo-labs/mtgo) that applies context-based deadlines to outgoing RPC calls, preventing requests from hanging forever.

The middleware wraps every outgoing RPC call with `context.WithTimeout`. When the configured timeout fires, the RPC context is cancelled and a typed `*TimeoutError` is returned. Existing caller deadlines are always respected — the middleware never extends them.

## Install

```bash
go get github.com/mtgo-labs/middlewares/timeout
```

## Quick start

```go
import (
    "time"

    tg "github.com/mtgo-labs/mtgo/telegram"
    "github.com/mtgo-labs/middlewares/timeout"
)

client, _ := tg.NewClient(apiID, apiHash, &tg.Config{BotToken: token})

// Apply a 30s timeout to all RPC calls.
client.UseInvokerMiddleware(timeout.Timeout(30 * time.Second))

client.Start()
```

## Configuration

```go
client.UseInvokerMiddleware(timeout.TimeoutConfig(timeout.TimeoutOptions{
    Default: 30 * time.Second,
    Methods: map[string]time.Duration{
        "messages.sendMessage": 10 * time.Second,
        "upload.saveFilePart":  2 * time.Minute,
    },
    DisabledMethods: []string{
        "updates.getDifference",
    },
}))
```

| Option | Description |
|--------|-------------|
| `Default` | Timeout for all RPC calls without a per-method override |
| `Methods` | Per-method timeout overrides (key = Telegram method name) |
| `DisabledMethods` | Methods exempt from any timeout (e.g. long-poll calls) |

### Priority

`DisabledMethods` > `Methods` > `Default`

If a method appears in `DisabledMethods`, no timeout is applied to it — even if it also has an entry in `Methods`.

### Caller deadlines

If the caller's `context.Context` already has a deadline that is sooner than the middleware's configured timeout, the caller's deadline governs. The middleware will **not**:

- Extend the caller's deadline
- Wrap the caller's `context.DeadlineExceeded` as `ErrTimeout`

The caller's deadline error passes through unchanged.

## Error handling

On timeout, the middleware returns a `*TimeoutError`:

```go
result, err := client.Invoke(ctx, req)
if err != nil {
    var timeoutErr *timeout.TimeoutError
    if errors.As(err, &timeoutErr) {
        log.Printf("RPC %s timed out after %s", timeoutErr.Method, timeoutErr.Timeout)
    }
}
```

`TimeoutError` satisfies:

- `errors.Is(err, timeout.ErrTimeout)` — middleware-specific sentinel
- `errors.Is(err, context.DeadlineExceeded)` — standard library compatibility

### FloodWait is not a timeout

Telegram `FLOOD_WAIT` errors pass through unwrapped. The timeout middleware does not treat rate-limit backoff as a local timeout. Use the [floodwait](../floodwait) middleware to handle retries.

## How this differs from…

### Client config `ReqTimeout`

mtgo's `Config.ReqTimeout` (default: 60s) sets the deadline for the internal session-level RPC send/receive cycle. The timeout middleware operates one layer above — it wraps the entire invoker chain, including any other middleware (ratelimit, floodwait). This means:

- `ReqTimeout` covers the wire-level round trip.
- The timeout middleware covers the full middleware chain + wire-level round trip.
- The middleware gives you **per-method** granularity that `ReqTimeout` cannot.

### FloodWait

`FLOOD_WAIT_<N>` is a server-side rate-limit response asking the client to wait N seconds. The timeout middleware explicitly ignores FloodWait errors — they are not timeouts. Use [floodwait](../floodwait) middleware to automatically sleep and retry on FloodWait.

### Retry middleware

The timeout middleware **does not retry**. When a timeout fires, it cancels the RPC context and returns the error immediately. Retry logic (re-sending the request after a failure) is handled by separate middleware like [floodwait](../floodwait). This separation lets you compose them:

```go
client.UseInvokerMiddleware(timeout.Timeout(30 * time.Second))           // outermost
client.UseInvokerMiddleware(floodwait.New().Middleware())                // retries
client.UseInvokerMiddleware(ratelimit.New(30, 10).Middleware())          // innermost
```

### Network reconnect logic

The timeout middleware only cancels the **specific RPC context** for the timed-out call. It does **not**:

- Close the client
- Drop the underlying connection
- Cancel other in-flight requests
- Trigger reconnection

The mtgo core handles connection-level issues (TCP drops, DC migrations) independently. When the middleware cancels a context, the core's pending-request map cleans up the entry safely — if the response arrives later, it is discarded.

## Design notes

- **Concurrency-safe**: the middleware holds only immutable config set at construction time. No locks needed on the hot path.
- **No goroutine leaks**: the middleware does not spawn goroutines. `context.WithTimeout` creates an internal timer that is stopped by `defer cancel()`.
- **No timer leaks**: `cancel()` is always called via `defer`, even on panic.
- **Method name resolution**: per-method overrides require resolving the Telegram method name from the TL constructor ID. This uses a reverse lookup of `tg.NamesMap`, built once and cached. In the common case (default timeout only, no per-method config), the lookup is skipped entirely.

## License

Apache License 2.0
