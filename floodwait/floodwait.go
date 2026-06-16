// Package floodwait provides invoker middleware that automatically handles
// Telegram FLOOD_WAIT errors by sleeping for the required duration and
// retrying the RPC call.
//
// Just register the middleware and all RPC calls are automatically protected.
// No handler changes required.
//
// Example:
//
//	waiter := floodwait.New()
//	waiter.OnWait(func(d time.Duration) {
//	    log.Printf("flood wait: %v", d)
//	})
//	client.UseInvokerMiddleware(waiter.Middleware())
package floodwait

import (
	"context"
	"sync"
	"time"

	"github.com/mtgo-labs/mtgo/tg"
	"github.com/mtgo-labs/mtgo/tgerr"
)

// Callback is invoked when a FLOOD_WAIT error is observed.
type Callback func(duration time.Duration)

// Middleware automatically retries RPC calls that fail with FLOOD_WAIT errors.
// It sleeps for the required duration and retries up to MaxRetries times.
//
// All RPC calls through the client are automatically protected — no handler
// changes required.
type Middleware struct {
	mu         sync.Mutex
	callback   Callback
	maxWait    time.Duration
	maxRetries int
}

// New creates a flood-wait middleware with sensible defaults:
//   - max wait: unlimited
//   - max retries: 5
func New() *Middleware {
	return &Middleware{
		maxRetries: 5,
	}
}

// OnWait sets a callback that fires when a FLOOD_WAIT is observed.
func (m *Middleware) OnWait(cb Callback) *Middleware {
	m.mu.Lock()
	m.callback = cb
	m.mu.Unlock()
	return m
}

// WithMaxWait sets the maximum acceptable flood wait duration.
// If Telegram asks to wait longer, the error is returned without retry.
// Default: unlimited.
func (m *Middleware) WithMaxWait(d time.Duration) *Middleware {
	m.mu.Lock()
	m.maxWait = d
	m.mu.Unlock()
	return m
}

// WithMaxRetries sets the maximum number of retry attempts per RPC call.
// Default: 5.
func (m *Middleware) WithMaxRetries(n int) *Middleware {
	m.mu.Lock()
	m.maxRetries = n
	m.mu.Unlock()
	return m
}

// Middleware returns a tg.InvokerMiddleware for UseInvokerMiddleware.
func (m *Middleware) Middleware() func(next tg.Invoker) tg.Invoker {
	return func(next tg.Invoker) tg.Invoker {
		return &wrappedInvoker{
			next: next,
			fn: func(ctx context.Context, input tg.TLObject, decode func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
				var retries int
				for {
					result, err := next.RPCInvoke(ctx, input, decode)
					if err == nil {
						return result, nil
					}

					d, ok := tgerr.AsFloodWait(err)
					if !ok {
						return result, err
					}

					retries++
					m.mu.Lock()
					maxR := m.maxRetries
					maxW := m.maxWait
					cb := m.callback
					m.mu.Unlock()

					if maxR > 0 && retries > maxR {
						return result, err
					}

					if d < time.Second {
						d = time.Second
					}
					if maxW > 0 && d > maxW {
						return result, err
					}

					if cb != nil {
						cb(d)
					}

					// Wait with 1s buffer (matches tgerr.FloodWait convention).
					timer := time.NewTimer(d + time.Second)
					select {
					case <-timer.C:
						// Retry.
					case <-ctx.Done():
						timer.Stop()
						return nil, ctx.Err()
					}
				}
			},
		}
	}
}

// wrappedInvoker implements tg.Invoker by intercepting RPCInvoke and
// forwarding RPCInvokeRaw transparently to the next invoker. This avoids
// the limitation of tg.InvokerFunc, which returns an error for raw calls.
type wrappedInvoker struct {
	next tg.Invoker
	fn   func(ctx context.Context, input tg.TLObject, decode func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error)
}

func (w *wrappedInvoker) RPCInvoke(ctx context.Context, input tg.TLObject, decode func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
	return w.fn(ctx, input, decode)
}

func (w *wrappedInvoker) RPCInvokeRaw(ctx context.Context, input tg.TLObject) ([]byte, error) {
	return w.next.RPCInvokeRaw(ctx, input)
}
