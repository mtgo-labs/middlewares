// Package ratelimit provides invoker middleware that throttles outgoing
// Telegram RPC calls using a token-bucket rate limiter.
//
// This prevents the bot from exceeding Telegram's API rate limits, which
// would trigger FLOOD_WAIT errors.
//
// Example:
//
//	limiter := ratelimit.New(rate.Every(100*time.Millisecond), 30)
//	client.UseInvokerMiddleware(limiter.Middleware())
package ratelimit

import (
	"context"

	"github.com/mtgo-labs/mtgo/tg"
	"golang.org/x/time/rate"
)

// Middleware rate-limits RPC calls using a token-bucket limiter.
type Middleware struct {
	lim *rate.Limiter
}

// New creates a rate-limiting middleware that allows up to r tokens per second
// with a burst size of b.
//
// Example:
//
//	// Allow at most 30 RPC calls per second, burst of 10.
//	mw := ratelimit.New(30, 10)
//	client.UseInvokerMiddleware(mw.Middleware())
//
//	// Allow 1 call per 100ms (10/sec), burst of 1.
//	mw := ratelimit.New(rate.Every(100*time.Millisecond), 1)
func New(r rate.Limit, b int) *Middleware {
	return &Middleware{lim: rate.NewLimiter(r, b)}
}

// Limiter returns the underlying rate.Limiter for runtime adjustment.
func (m *Middleware) Limiter() *rate.Limiter {
	return m.lim
}

// Middleware returns a tg.InvokerMiddleware function for UseInvokerMiddleware.
func (m *Middleware) Middleware() func(next tg.Invoker) tg.Invoker {
	return func(next tg.Invoker) tg.Invoker {
		return &invoker{lim: m.lim, next: next}
	}
}

// invoker implements tg.Invoker, applying rate limiting to both decoded
// (RPCInvoke) and raw (RPCInvokeRaw) RPC paths.
type invoker struct {
	lim  *rate.Limiter
	next tg.Invoker
}

func (i *invoker) RPCInvoke(ctx context.Context, input tg.TLObject, decode func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
	if err := i.lim.Wait(ctx); err != nil {
		return nil, err
	}
	return i.next.RPCInvoke(ctx, input, decode)
}

func (i *invoker) RPCInvokeRaw(ctx context.Context, input tg.TLObject) ([]byte, error) {
	if err := i.lim.Wait(ctx); err != nil {
		return nil, err
	}
	return i.next.RPCInvokeRaw(ctx, input)
}
