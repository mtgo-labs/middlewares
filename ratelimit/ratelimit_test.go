package ratelimit_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mtgo-labs/middlewares/ratelimit"
	"github.com/mtgo-labs/mtgo/tg"
	"golang.org/x/time/rate"
)

type mockInvoker struct {
	fn    func(context.Context, tg.TLObject, func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error)
	rawFn func(context.Context, tg.TLObject) ([]byte, error)
}

func (m *mockInvoker) RPCInvoke(ctx context.Context, input tg.TLObject, decode func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
	return m.fn(ctx, input, decode)
}

func (m *mockInvoker) RPCInvokeRaw(ctx context.Context, input tg.TLObject) ([]byte, error) {
	if m.rawFn != nil {
		return m.rawFn(ctx, input)
	}
	return nil, nil
}

func TestNew(t *testing.T) {
	mw := ratelimit.New(rate.Limit(10), 5)
	if mw == nil {
		t.Fatal("expected non-nil middleware")
	}
}

func TestLimiter(t *testing.T) {
	mw := ratelimit.New(rate.Limit(10), 5)
	if mw.Limiter() == nil {
		t.Fatal("expected non-nil limiter")
	}
}

func TestPassthrough(t *testing.T) {
	var called int32
	base := &mockInvoker{fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
		atomic.AddInt32(&called, 1)
		return nil, nil
	}}

	mw := ratelimit.New(rate.Limit(100), 10)
	invoker := mw.Middleware()(base)

	_, err := invoker.RPCInvoke(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Error("expected invoker to be called")
	}
}

func TestRateLimiting(t *testing.T) {
	mw := ratelimit.New(rate.Limit(1), 1)
	invoker := mw.Middleware()(&mockInvoker{fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
		return nil, nil
	}})

	start := time.Now()
	invoker.RPCInvoke(context.Background(), nil, nil) // First call: immediate.
	invoker.RPCInvoke(context.Background(), nil, nil) // Second call: waits ~1s.

	if elapsed := time.Since(start); elapsed < 800*time.Millisecond {
		t.Errorf("expected rate limiting delay, elapsed: %v", elapsed)
	}
}

func TestContextCancellation(t *testing.T) {
	mw := ratelimit.New(rate.Limit(0.1), 1)
	invoker := mw.Middleware()(&mockInvoker{fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
		return nil, nil
	}})

	// Exhaust burst.
	invoker.RPCInvoke(context.Background(), nil, nil)

	// Cancelled context.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := invoker.RPCInvoke(ctx, nil, nil)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestConcurrent(t *testing.T) {
	mw := ratelimit.New(rate.Limit(50), 10)
	invoker := mw.Middleware()(&mockInvoker{fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
		return nil, nil
	}})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			invoker.RPCInvoke(context.Background(), nil, nil)
		}()
	}
	wg.Wait()
}

func TestRPCInvokeRawPassthrough(t *testing.T) {
	var rawCalled int32
	base := &mockInvoker{
		fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			t.Fatal("RPCInvoke should not be called")
			return nil, nil
		},
		rawFn: func(_ context.Context, _ tg.TLObject) ([]byte, error) {
			atomic.AddInt32(&rawCalled, 1)
			return []byte("raw"), nil
		},
	}

	mw := ratelimit.New(rate.Limit(100), 10)
	invoker := mw.Middleware()(base)
	data, err := invoker.RPCInvokeRaw(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if string(data) != "raw" {
		t.Errorf("expected raw passthrough data, got: %q", data)
	}
	if atomic.LoadInt32(&rawCalled) != 1 {
		t.Errorf("expected raw to be called once, got %d", rawCalled)
	}
}
