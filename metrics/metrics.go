// Package metrics provides invoker middleware that collects RPC-level metrics
// for mtgo clients without affecting request behaviour.
//
// The middleware wraps every outgoing RPC call, recording the Telegram method
// name, request status (success, error, flood_wait, timeout, cancelled), and
// round-trip latency. It is safe for concurrent use and adds minimal overhead.
//
// Example:
//
//	mc := metrics.NewMemoryCollector()
//	mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})
//	client.UseInvokerMiddleware(mw.Middleware())
package metrics

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mtgo-labs/mtgo/tg"
	"github.com/mtgo-labs/mtgo/tgerr"
)

// Request status values used as metric labels.
const (
	StatusSuccess   = "success"
	StatusError     = "error"
	StatusFloodWait = "flood_wait"
	StatusTimeout   = "timeout"
	StatusCancelled = "cancelled"
)

// Collector is the interface implemented by metrics backends.
// All methods must be safe for concurrent use.
//
//   - [MemoryCollector] — in-memory, for tests and ephemeral bots.
//   - [PrometheusCollector] — Prometheus-compatible, for production scraping.
type Collector interface {
	// IncRequests increments the total RPC request counter for the given
	// method and status (one of the Status* constants).
	IncRequests(method, status string)

	// ObserveLatency records the round-trip latency of an RPC call.
	ObserveLatency(method string, d time.Duration)

	// IncInFlight increments the in-flight gauge for the given method.
	IncInFlight(method string)

	// DecInFlight decrements the in-flight gauge for the given method.
	DecInFlight(method string)

	// IncFloodWait increments the flood-wait counter for the given method.
	IncFloodWait(method string)

	// IncTimeout increments the timeout/cancelled counter for the given method.
	IncTimeout(method string)

	// IncRetry increments the retry counter for the given method.
	// Called by retry middleware when one is registered; not invoked by the
	// metrics middleware itself.
	IncRetry(method string)
}

// NopCollector is a no-op Collector that discards all metrics.
type NopCollector struct{}

func (NopCollector) IncRequests(string, string)            {}
func (NopCollector) ObserveLatency(string, time.Duration)   {}
func (NopCollector) IncInFlight(string)                     {}
func (NopCollector) DecInFlight(string)                     {}
func (NopCollector) IncFloodWait(string)                    {}
func (NopCollector) IncTimeout(string)                      {}
func (NopCollector) IncRetry(string)                        {}

// Config configures the metrics middleware.
type Config struct {
	// EnableMethodLabels records the Telegram method name as a metric label.
	// When false, all methods are recorded as "unknown".
	// Default: true.
	EnableMethodLabels bool
}

// Middleware collects RPC metrics for all outgoing Telegram RPC calls.
// It wraps the invoker chain, recording method, status, and latency without
// modifying the result or error returned to the caller.
type Middleware struct {
	c             Collector
	enableMethods bool
}

// New creates a metrics middleware backed by collector.
// If collector is nil, a [NopCollector] is used.
func New(collector Collector, cfg Config) *Middleware {
	if collector == nil {
		collector = NopCollector{}
	}
	return &Middleware{
		c:             collector,
		enableMethods: cfg.EnableMethodLabels,
	}
}

// Collector returns the underlying Collector, allowing callers to read
// collected metrics (e.g. from a [MemoryCollector] in tests).
func (m *Middleware) Collector() Collector { return m.c }

// Middleware returns a function suitable for [Client.UseInvokerMiddleware].
func (m *Middleware) Middleware() func(next tg.Invoker) tg.Invoker {
	return func(next tg.Invoker) tg.Invoker {
		return &invoker{m: m, next: next}
	}
}

// invoker implements [tg.Invoker], wrapping RPC calls with metric recording.
type invoker struct {
	m    *Middleware
	next tg.Invoker
}

func (i *invoker) RPCInvoke(ctx context.Context, input tg.TLObject, decode func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
	method := i.m.method(input)
	i.m.c.IncInFlight(method)
	defer i.m.c.DecInFlight(method)

	start := time.Now()
	result, err := i.next.RPCInvoke(ctx, input, decode)
	i.m.record(method, start, err)
	return result, err
}

func (i *invoker) RPCInvokeRaw(ctx context.Context, input tg.TLObject) ([]byte, error) {
	method := i.m.method(input)
	i.m.c.IncInFlight(method)
	defer i.m.c.DecInFlight(method)

	start := time.Now()
	result, err := i.next.RPCInvokeRaw(ctx, input)
	i.m.record(method, start, err)
	return result, err
}

// record observes latency and increments request/flood/timeout counters.
func (m *Middleware) record(method string, start time.Time, err error) {
	m.c.ObserveLatency(method, time.Since(start))

	status := classify(err)
	m.c.IncRequests(method, status)

	switch status {
	case StatusFloodWait:
		m.c.IncFloodWait(method)
	case StatusTimeout, StatusCancelled:
		m.c.IncTimeout(method)
	}
}

// classify maps an error to a status label.
func classify(err error) string {
	if err == nil {
		return StatusSuccess
	}
	if _, ok := tgerr.AsFloodWait(err); ok {
		return StatusFloodWait
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return StatusTimeout
	}
	if errors.Is(err, context.Canceled) {
		return StatusCancelled
	}
	return StatusError
}

var (
	nameMapOnce sync.Once
	idToName    map[uint32]string
)

// initNameMap builds a reverse lookup from constructor ID to TL qualified name
// (e.g. "help.getConfig") using [tg.NamesMap].
func initNameMap() {
	idToName = make(map[uint32]string, len(tg.NamesMap))
	for name, id := range tg.NamesMap {
		idToName[id] = name
	}
}

// methodName resolves the Telegram method name for a TLObject.
// Returns "unknown" if the object is nil or the constructor ID is not recognised.
func methodName(input tg.TLObject) string {
	if input == nil {
		return "unknown"
	}
	nameMapOnce.Do(initNameMap)
	if name, ok := idToName[input.ConstructorID()]; ok {
		return name
	}
	return "unknown"
}

// method returns the metric label for the method, or "unknown" when method
// labels are disabled.
func (m *Middleware) method(input tg.TLObject) string {
	if !m.enableMethods {
		return "unknown"
	}
	return methodName(input)
}
