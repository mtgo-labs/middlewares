# metrics

Invoker middleware for [mtgo](https://github.com/mtgo-labs/mtgo) that collects RPC-level metrics without changing request behaviour.

Every outgoing RPC call is wrapped to record the Telegram method name, request status, and latency. The middleware is safe for concurrent use, adds minimal overhead, and never modifies the result or error returned to the caller.

## Install

```bash
go get github.com/mtgo-labs/middlewares/metrics
```

## Quick start

```go
import (
    "database/sql"

    tg "github.com/mtgo-labs/mtgo/telegram"
    "github.com/mtgo-labs/middlewares/metrics"
    _ "modernc.org/sqlite"
)

client, _ := tg.NewClient(apiID, apiHash, &tg.Config{BotToken: token})

db, _ := sql.Open("sqlite", "bot.db")
mc, _ := metrics.NewSQLiteCollector(db)
defer mc.Close()
mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})
client.UseInvokerMiddleware(mw.Middleware())
```

## Metrics collected

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `rpc_requests_total` | counter | `method`, `status` | Total RPC requests by method and status |
| `rpc_latency_seconds` | histogram | `method` | RPC round-trip latency |
| `rpc_in_flight` | gauge | `method` | Currently in-flight RPC requests |
| `rpc_flood_wait_total` | counter | `method` | FLOOD_WAIT errors |
| `rpc_timeout_total` | counter | `method` | Timed-out or cancelled requests |
| `rpc_retries_total` | counter | `method` | RPC retries (populated by retry middleware) |

### Status values

| Status | Condition |
|--------|-----------|
| `success` | RPC returned no error |
| `error` | RPC returned an error (non-flood, non-timeout) |
| `flood_wait` | FLOOD_WAIT / FLOOD_PREMIUM_WAIT |
| `timeout` | `context.DeadlineExceeded` |
| `cancelled` | `context.Canceled` |

## Collectors

The middleware uses a `Collector` interface so any backend can be plugged in.

### SQLite (recommended for production)

Persists cumulative counters to a SQL database with automatic schema migration (`CREATE TABLE IF NOT EXISTS`). Counters are kept in memory for zero-latency writes on the RPC hot path; a background goroutine snapshots to the database at a configurable interval. On startup, previous counters are loaded so metrics survive process restarts.

```go
import (
    "database/sql"

    "github.com/mtgo-labs/middlewares/metrics"
    _ "modernc.org/sqlite"
)

db, _ := sql.Open("sqlite", "bot.db")
mc, _ := metrics.NewSQLiteCollector(db)
defer mc.Close()

mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})
client.UseInvokerMiddleware(mw.Middleware())
```

You can share the same database file as your session storage — the metrics table (`rpc_metrics`) is independent and won't conflict with session tables.

#### Schema

```sql
CREATE TABLE IF NOT EXISTS rpc_metrics (
    method          TEXT    NOT NULL,
    status          TEXT    NOT NULL DEFAULT '',
    requests        INTEGER NOT NULL DEFAULT 0,
    flood_waits     INTEGER NOT NULL DEFAULT 0,
    timeouts        INTEGER NOT NULL DEFAULT 0,
    retries         INTEGER NOT NULL DEFAULT 0,
    latency_sum_us  INTEGER NOT NULL DEFAULT 0,
    latency_count   INTEGER NOT NULL DEFAULT 0,
    updated_at      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (method, status)
)
```

#### Options

```go
// Flush every 30 seconds instead of the default 10s.
mc, _ := metrics.NewSQLiteCollector(db, metrics.WithFlushInterval(30*time.Second))
```

#### Querying stored metrics

```go
// Programmatic access (reads from in-memory state, always current):
mc.Requests("help.getConfig", "success")
mc.FloodWaits("messages.sendMessage")
mc.LatencyAvg("upload.getFile")  // average latency across all calls

// Or query the database directly:
// SELECT method, status, requests FROM rpc_metrics WHERE method = 'help.getConfig'
```

### In-memory

Stores counters and latency samples in mutex-protected maps. Ideal for tests and ephemeral bots.

```go
mc := metrics.NewMemoryCollector()
mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})

// ... run RPCs ...

fmt.Println(mc.Requests("help.getConfig", "success"))
fmt.Println(mc.FloodWaits("messages.sendMessage"))
fmt.Println(mc.InFlight("upload.getFile"))
```

### Prometheus

Creates standard Prometheus counters, histograms, and gauges. Register them with your Prometheus registry:

```go
import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/mtgo-labs/middlewares/metrics"
)

pc := metrics.NewPrometheusCollector("mtgo")
prometheus.MustRegister(pc.Collectors()...)

mw := metrics.New(pc, metrics.Config{EnableMethodLabels: true})
client.UseInvokerMiddleware(mw.Middleware())
```

Produces metrics like:

```
mtgo_rpc_requests_total{method="messages.sendMessage",status="success"} 42
mtgo_rpc_latency_seconds_bucket{method="help.getConfig",le="0.05"} 38
mtgo_rpc_in_flight{method="upload.getFile"} 3
mtgo_rpc_flood_wait_total{method="messages.sendMessage"} 1
mtgo_rpc_timeout_total{method="users.getUsers"} 0
```

## Configuration

```go
type Config struct {
    // EnableMethodLabels records the Telegram method name as a metric label.
    // When false, all methods are recorded as "unknown".
    // Default: true.
    EnableMethodLabels bool
}
```

Method names are resolved from TL constructor IDs via `tg.NamesMap`. The reverse lookup is built once (lazily on first use) and cached.

## Composition with other middlewares

Middlewares compose in registration order. Register metrics first (outermost) so it observes the final result including retries from floodwait:

```go
client.UseInvokerMiddleware(mw.Middleware())             // metrics (outermost)
client.UseInvokerMiddleware(floodwait.New().Middleware()) // retries flood waits
client.UseInvokerMiddleware(ratelimit.New(30, 10).Middleware())
```

## Custom collectors

Implement the `Collector` interface for OpenTelemetry, StatsD, or any other backend:

```go
type Collector interface {
    IncRequests(method, status string)
    ObserveLatency(method string, d time.Duration)
    IncInFlight(method string)
    DecInFlight(method string)
    IncFloodWait(method string)
    IncTimeout(method string)
    IncRetry(method string)
}
```

All methods must be safe for concurrent use.

## License

Apache License 2.0
