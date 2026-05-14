package floodwait_test

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mtgo-labs/mtgo/tg"
	"github.com/mtgo-labs/mtgo/tgerr"
	"github.com/mtgo-labs/middlewares/floodwait"
)

type mockInvoker struct {
	fn func(context.Context, tg.TLObject, func(io.Reader) (tg.TLObject, error)) (tg.TLObject, error)
}

func (m *mockInvoker) RPCInvoke(ctx context.Context, input tg.TLObject, decode func(io.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
	return m.fn(ctx, input, decode)
}

func TestNew(t *testing.T) {
	mw := floodwait.New()
	if mw == nil {
		t.Fatal("expected non-nil middleware")
	}
}

func TestPassthrough(t *testing.T) {
	var called int32
	base := &mockInvoker{fn: func(_ context.Context, _ tg.TLObject, _ func(io.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
		atomic.AddInt32(&called, 1)
		return nil, nil
	}}

	invoker := floodwait.New().Middleware()(base)
	_, err := invoker.RPCInvoke(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Error("expected invoker to be called once")
	}
}

func TestNonFloodError(t *testing.T) {
	expectedErr := errors.New("some error")
	base := &mockInvoker{fn: func(_ context.Context, _ tg.TLObject, _ func(io.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
		return nil, expectedErr
	}}

	invoker := floodwait.New().Middleware()(base)
	_, err := invoker.RPCInvoke(context.Background(), nil, nil)
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected original error, got: %v", err)
	}
}

func TestRetriesOnFloodWait(t *testing.T) {
	var attempts int32
	base := &mockInvoker{fn: func(_ context.Context, _ tg.TLObject, _ func(io.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			return nil, tgerr.New(420, "FLOOD_WAIT_1")
		}
		return tg.TLObject(nil), nil // Success on retry.
	}}

	invoker := floodwait.New().WithMaxRetries(3).Middleware()(base)

	start := time.Now()
	_, err := invoker.RPCInvoke(context.Background(), nil, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 2 {
		t.Errorf("expected 2 attempts, got %d", atomic.LoadInt32(&attempts))
	}
	// FLOOD_WAIT_1 = 1s + 1s buffer = ~2s wait.
	if elapsed < 1500*time.Millisecond {
		t.Errorf("expected flood wait delay, elapsed: %v", elapsed)
	}
}

func TestCallback(t *testing.T) {
	var received time.Duration
	var attempts int32
	mw := floodwait.New().OnWait(func(d time.Duration) {
		received = d
	})

	base := &mockInvoker{fn: func(_ context.Context, _ tg.TLObject, _ func(io.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			return nil, tgerr.New(420, "FLOOD_WAIT_1")
		}
		return tg.TLObject(nil), nil
	}}

	invoker := mw.Middleware()(base)
	invoker.RPCInvoke(context.Background(), nil, nil)

	if received != 1*time.Second {
		t.Errorf("callback received %v, want 1s", received)
	}
}

func TestMaxRetriesExceeded(t *testing.T) {
	var attempts int32
	base := &mockInvoker{fn: func(_ context.Context, _ tg.TLObject, _ func(io.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, tgerr.New(420, "FLOOD_WAIT_1")
	}}

	invoker := floodwait.New().WithMaxRetries(2).Middleware()(base)
	_, err := invoker.RPCInvoke(context.Background(), nil, nil)

	if err == nil {
		t.Error("expected error after max retries exceeded")
	}
	if atomic.LoadInt32(&attempts) != 3 { // Initial + 2 retries.
		t.Errorf("expected 3 attempts, got %d", atomic.LoadInt32(&attempts))
	}
}

func TestMaxWaitExceeded(t *testing.T) {
	var called int32
	base := &mockInvoker{fn: func(_ context.Context, _ tg.TLObject, _ func(io.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
		atomic.AddInt32(&called, 1)
		return nil, tgerr.New(420, "FLOOD_WAIT_60")
	}}

	invoker := floodwait.New().WithMaxWait(5 * time.Second).Middleware()(base)
	_, err := invoker.RPCInvoke(context.Background(), nil, nil)

	if err == nil {
		t.Error("expected error when wait exceeds maxWait")
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Error("should have been called only once without retrying")
	}
}

func TestContextCancellation(t *testing.T) {
	var attempts int32
	base := &mockInvoker{fn: func(_ context.Context, _ tg.TLObject, _ func(io.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, tgerr.New(420, "FLOOD_WAIT_30")
	}}

	invoker := floodwait.New().Middleware()(base)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := invoker.RPCInvoke(ctx, nil, nil)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestMultipleFloodWaits(t *testing.T) {
	var attempts int32
	base := &mockInvoker{fn: func(_ context.Context, _ tg.TLObject, _ func(io.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n <= 2 {
			return nil, tgerr.New(420, "FLOOD_WAIT_1")
		}
		return tg.TLObject(nil), nil
	}}

	invoker := floodwait.New().WithMaxRetries(5).Middleware()(base)
	_, err := invoker.RPCInvoke(context.Background(), nil, nil)

	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected 3 attempts, got %d", atomic.LoadInt32(&attempts))
	}
}
