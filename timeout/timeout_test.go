package timeout_test

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mtgo-labs/middlewares/timeout"
	"github.com/mtgo-labs/mtgo/tg"
	"github.com/mtgo-labs/mtgo/tgerr"
)

// --- Test helpers ---

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

type mockTLObject struct {
	id uint32
}

func (m *mockTLObject) Encode(*bytes.Buffer) error { return nil }
func (m *mockTLObject) ConstructorID() uint32      { return m.id }

// objWithMethod creates a TLObject whose constructor ID maps to the given
// Telegram method name via tg.NamesMap.
func objWithMethod(method string) *mockTLObject {
	return &mockTLObject{id: tg.NamesMap[method]}
}

// blockingInvoker blocks until ctx.Done(), then returns ctx.Err().
func blockingInvoker() *mockInvoker {
	return &mockInvoker{
		fn: func(ctx context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		rawFn: func(ctx context.Context, _ tg.TLObject) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
}

// --- Tests ---

func TestTimeout_Default(t *testing.T) {
	base := blockingInvoker()
	invoker := timeout.Timeout(50 * time.Millisecond)(base)

	start := time.Now()
	_, err := invoker.RPCInvoke(context.Background(), nil, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("timeout fired too late: %v", elapsed)
	}
	if !errors.Is(err, timeout.ErrTimeout) {
		t.Errorf("expected errors.Is(err, ErrTimeout), got: %v", err)
	}
}

func TestTimeout_SuccessBeforeTimeout(t *testing.T) {
	var called int32
	base := &mockInvoker{
		fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			atomic.AddInt32(&called, 1)
			return nil, nil
		},
	}
	invoker := timeout.Timeout(5 * time.Second)(base)

	_, err := invoker.RPCInvoke(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Error("expected invoker to be called once")
	}
}

func TestTimeout_PerMethodOverride(t *testing.T) {
	// Per-method override: 50ms for the test method, default 10s.
	// The override must fire (not the default).
	base := blockingInvoker()
	invoker := timeout.TimeoutConfig(timeout.TimeoutOptions{
		Default: 10 * time.Second,
		Methods: map[string]time.Duration{
			"help.getConfig": 50 * time.Millisecond,
		},
	})(base)

	input := objWithMethod("help.getConfig")
	start := time.Now()
	_, err := invoker.RPCInvoke(context.Background(), input, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("per-method timeout fired too late: %v", elapsed)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("per-method timeout fired too early: %v", elapsed)
	}
	if !errors.Is(err, timeout.ErrTimeout) {
		t.Errorf("expected ErrTimeout, got: %v", err)
	}
}

func TestTimeout_CallerDeadlineShorter(t *testing.T) {
	base := blockingInvoker()
	invoker := timeout.Timeout(10 * time.Second)(base)

	// Caller sets a 50ms deadline — much shorter than middleware's 10s.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := invoker.RPCInvoke(ctx, nil, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error")
	}
	// The caller's deadline should fire, not the middleware's.
	if elapsed > 500*time.Millisecond {
		t.Errorf("caller deadline fired too late: %v", elapsed)
	}
	// Should NOT be wrapped as ErrTimeout — it's the caller's deadline.
	if errors.Is(err, timeout.ErrTimeout) {
		t.Errorf("caller deadline should not be wrapped as ErrTimeout: %v", err)
	}
	// Should be plain context.DeadlineExceeded.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
}

func TestTimeout_CallerDeadlineLonger(t *testing.T) {
	base := blockingInvoker()
	invoker := timeout.Timeout(50 * time.Millisecond)(base)

	// Caller sets a 10s deadline — much longer than middleware's 50ms.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	_, err := invoker.RPCInvoke(ctx, nil, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("middleware timeout fired too late: %v", elapsed)
	}
	// Middleware's timeout should fire, so it IS wrapped as ErrTimeout.
	if !errors.Is(err, timeout.ErrTimeout) {
		t.Errorf("expected ErrTimeout: %v", err)
	}
}

func TestTimeout_DisabledMethod(t *testing.T) {
	var fastReturn int32
	base := &mockInvoker{
		fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			atomic.AddInt32(&fastReturn, 1)
			return nil, nil // immediate success
		},
	}

	invoker := timeout.TimeoutConfig(timeout.TimeoutOptions{
		Default: 50 * time.Millisecond,
		DisabledMethods: []string{
			"updates.getDifference",
		},
	})(base)

	input := objWithMethod("updates.getDifference")
	_, err := invoker.RPCInvoke(context.Background(), input, nil)
	if err != nil {
		t.Fatalf("disabled method should not timeout: %v", err)
	}
	if atomic.LoadInt32(&fastReturn) != 1 {
		t.Error("expected invoker called once")
	}
}

func TestTimeout_DisabledMethodStillBlocks(t *testing.T) {
	// Verify disabled methods truly have no middleware timeout — they only
	// respond to the caller's own context deadline.
	base := blockingInvoker()

	invoker := timeout.TimeoutConfig(timeout.TimeoutOptions{
		Default: 50 * time.Millisecond,
		DisabledMethods: []string{
			"updates.getDifference",
		},
	})(base)

	input := objWithMethod("updates.getDifference")

	// Use a short caller deadline so the test doesn't hang forever.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := invoker.RPCInvoke(ctx, input, nil)
	// Should be the caller's deadline, not our timeout.
	if err == nil {
		t.Fatal("expected error from caller deadline")
	}
	if errors.Is(err, timeout.ErrTimeout) {
		t.Errorf("disabled method should not produce ErrTimeout: %v", err)
	}
}

func TestTimeout_ErrorType(t *testing.T) {
	base := blockingInvoker()
	invoker := timeout.Timeout(50 * time.Millisecond)(base)

	input := objWithMethod("help.getConfig")
	_, err := invoker.RPCInvoke(context.Background(), input, nil)

	var timeoutErr *timeout.TimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("expected *TimeoutError, got: %T — %v", err, err)
	}
	if timeoutErr.Method != "help.getConfig" {
		t.Errorf("expected method 'help.getConfig', got %q", timeoutErr.Method)
	}
	if timeoutErr.Timeout != 50*time.Millisecond {
		t.Errorf("expected timeout 50ms, got %v", timeoutErr.Timeout)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Error("expected errors.Is(err, context.DeadlineExceeded)")
	}
	if !errors.Is(err, timeout.ErrTimeout) {
		t.Error("expected errors.Is(err, ErrTimeout)")
	}
}

func TestTimeout_FloodWaitNotTimeout(t *testing.T) {
	base := &mockInvoker{
		fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			return nil, tgerr.New(420, "FLOOD_WAIT_5")
		},
	}
	invoker := timeout.Timeout(50 * time.Millisecond)(base)

	_, err := invoker.RPCInvoke(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	// Must NOT be wrapped as ErrTimeout.
	if errors.Is(err, timeout.ErrTimeout) {
		t.Errorf("FLOOD_WAIT should not be treated as timeout: %v", err)
	}
	// Should be the original FLOOD_WAIT error.
	if _, ok := tgerr.AsFloodWait(err); !ok {
		t.Errorf("expected FLOOD_WAIT error to pass through: %v", err)
	}
}

func TestTimeout_NoConfig(t *testing.T) {
	var called int32
	base := &mockInvoker{
		fn: func(ctx context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			atomic.AddInt32(&called, 1)
			// Verify no deadline was applied.
			if _, ok := ctx.Deadline(); ok {
				t.Error("expected no deadline on context")
			}
			return nil, nil
		},
	}
	// No default, no per-method, no disabled — pure passthrough.
	invoker := timeout.TimeoutConfig(timeout.TimeoutOptions{})(base)

	_, err := invoker.RPCInvoke(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Error("expected invoker called once")
	}
}

func TestTimeout_RPCInvokeRaw(t *testing.T) {
	base := blockingInvoker()
	invoker := timeout.Timeout(50 * time.Millisecond)(base)

	start := time.Now()
	_, err := invoker.RPCInvokeRaw(context.Background(), nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("timeout fired too late: %v", elapsed)
	}
	if !errors.Is(err, timeout.ErrTimeout) {
		t.Errorf("expected ErrTimeout: %v", err)
	}
}

func TestTimeout_RPCInvokeRawSuccess(t *testing.T) {
	var called int32
	base := &mockInvoker{
		rawFn: func(_ context.Context, _ tg.TLObject) ([]byte, error) {
			atomic.AddInt32(&called, 1)
			return []byte("ok"), nil
		},
	}
	invoker := timeout.Timeout(5 * time.Second)(base)

	result, err := invoker.RPCInvokeRaw(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != "ok" {
		t.Errorf("expected 'ok', got %q", string(result))
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Error("expected invoker called once")
	}
}

func TestTimeout_NoGoroutineLeak(t *testing.T) {
	base := &mockInvoker{
		fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			return nil, nil
		},
	}
	invoker := timeout.Timeout(5 * time.Second)(base)

	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	before := runtime.NumGoroutine()

	const n = 2000
	for i := 0; i < n; i++ {
		_, _ = invoker.RPCInvoke(context.Background(), nil, nil)
	}

	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()

	if after > before+5 {
		t.Errorf("goroutine leak: before=%d after=%d (after %d invocations)", before, after, n)
	}
}

func TestTimeout_ConcurrentSafe(t *testing.T) {
	var success atomic.Int64
	base := &mockInvoker{
		fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			success.Add(1)
			return nil, nil
		},
	}
	invoker := timeout.Timeout(5 * time.Second)(base)

	var wg sync.WaitGroup
	const goroutines = 50
	const callsEach = 20

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := 0; c < callsEach; c++ {
				_, _ = invoker.RPCInvoke(context.Background(), nil, nil)
			}
		}()
	}
	wg.Wait()

	if got := success.Load(); got != goroutines*callsEach {
		t.Errorf("expected %d calls, got %d", goroutines*callsEach, got)
	}
}
