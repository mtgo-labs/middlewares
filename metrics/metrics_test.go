package metrics_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mtgo-labs/middlewares/metrics"
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

func successInvoker() *mockInvoker {
	return &mockInvoker{
		fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			return nil, nil
		},
	}
}

// --- Base tests ---

func TestNew(t *testing.T) {
	mw := metrics.New(metrics.NewMemoryCollector(), metrics.Config{EnableMethodLabels: true})
	if mw == nil {
		t.Fatal("expected non-nil middleware")
	}
}

func TestNilCollector(t *testing.T) {
	mw := metrics.New(nil, metrics.Config{})
	inv := mw.Middleware()(successInvoker())
	_, err := inv.RPCInvoke(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCollector(t *testing.T) {
	mc := metrics.NewMemoryCollector()
	mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})
	if mw.Collector() != mc {
		t.Fatal("Collector() should return the same instance")
	}
}

func TestSuccess(t *testing.T) {
	mc := metrics.NewMemoryCollector()
	mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})
	input := &mockTLObject{id: tg.HelpGetConfigTypeID}
	inv := mw.Middleware()(successInvoker())

	_, err := inv.RPCInvoke(context.Background(), input, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := mc.Requests("help.getConfig", metrics.StatusSuccess); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
	if got := mc.InFlight("help.getConfig"); got != 0 {
		t.Errorf("inFlight = %d, want 0", got)
	}
	if lats := mc.Latencies("help.getConfig"); len(lats) != 1 {
		t.Errorf("latencies = %d samples, want 1", len(lats))
	}
}

func TestError(t *testing.T) {
	mc := metrics.NewMemoryCollector()
	mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})
	rpcErr := errors.New("internal server error")
	base := &mockInvoker{
		fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			return nil, rpcErr
		},
	}
	inv := mw.Middleware()(base)

	_, err := inv.RPCInvoke(context.Background(), nil, nil)
	if !errors.Is(err, rpcErr) {
		t.Fatalf("error should pass through, got: %v", err)
	}
	if got := mc.Requests("unknown", metrics.StatusError); got != 1 {
		t.Errorf("requests[unknown,error] = %d, want 1", got)
	}
}

func TestFloodWait(t *testing.T) {
	mc := metrics.NewMemoryCollector()
	mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})
	base := &mockInvoker{
		fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			return nil, tgerr.New(420, "FLOOD_WAIT_30")
		},
	}
	inv := mw.Middleware()(base)

	_, err := inv.RPCInvoke(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected flood wait error")
	}
	if got := mc.Requests("unknown", metrics.StatusFloodWait); got != 1 {
		t.Errorf("requests[flood_wait] = %d, want 1", got)
	}
	if got := mc.FloodWaits("unknown"); got != 1 {
		t.Errorf("floodWaits = %d, want 1", got)
	}
}

func TestTimeout(t *testing.T) {
	mc := metrics.NewMemoryCollector()
	mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})
	base := &mockInvoker{
		fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			return nil, context.DeadlineExceeded
		},
	}
	inv := mw.Middleware()(base)

	_, _ = inv.RPCInvoke(context.Background(), nil, nil)
	if got := mc.Requests("unknown", metrics.StatusTimeout); got != 1 {
		t.Errorf("requests[timeout] = %d, want 1", got)
	}
	if got := mc.Timeouts("unknown"); got != 1 {
		t.Errorf("timeouts = %d, want 1", got)
	}
}

func TestCancelled(t *testing.T) {
	mc := metrics.NewMemoryCollector()
	mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})
	base := &mockInvoker{
		fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			return nil, context.Canceled
		},
	}
	inv := mw.Middleware()(base)

	_, _ = inv.RPCInvoke(context.Background(), nil, nil)
	if got := mc.Requests("unknown", metrics.StatusCancelled); got != 1 {
		t.Errorf("requests[cancelled] = %d, want 1", got)
	}
	if got := mc.Timeouts("unknown"); got != 1 {
		t.Errorf("timeouts = %d, want 1", got)
	}
}

func TestInFlightDuringCall(t *testing.T) {
	mc := metrics.NewMemoryCollector()
	mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})

	blocking := make(chan struct{})
	var observed int64
	base := &mockInvoker{
		fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			atomic.StoreInt64(&observed, mc.InFlight("help.getConfig"))
			close(blocking)
			return nil, nil
		},
	}
	inv := mw.Middleware()(base)
	input := &mockTLObject{id: tg.HelpGetConfigTypeID}

	go func() { _, _ = inv.RPCInvoke(context.Background(), input, nil) }()
	<-blocking

	if got := atomic.LoadInt64(&observed); got != 1 {
		t.Errorf("inFlight during call = %d, want 1", got)
	}
	if got := mc.InFlight("help.getConfig"); got != 0 {
		t.Errorf("inFlight after call = %d, want 0", got)
	}
}

func TestConcurrent(t *testing.T) {
	mc := metrics.NewMemoryCollector()
	mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})
	inv := mw.Middleware()(successInvoker())
	input := &mockTLObject{id: tg.HelpGetConfigTypeID}

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = inv.RPCInvoke(context.Background(), input, nil)
		}()
	}
	wg.Wait()

	if got := mc.Requests("help.getConfig", metrics.StatusSuccess); got != n {
		t.Errorf("requests = %d, want %d", got, n)
	}
}

func TestRPCInvokeRaw(t *testing.T) {
	mc := metrics.NewMemoryCollector()
	mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})
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
	inv := mw.Middleware()(base)
	input := &mockTLObject{id: tg.HelpGetConfigTypeID}

	data, err := inv.RPCInvokeRaw(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "raw" {
		t.Errorf("data = %q, want %q", string(data), "raw")
	}
	if atomic.LoadInt32(&rawCalled) != 1 {
		t.Errorf("raw called %d times, want 1", rawCalled)
	}
}

func TestMethodLabelDisabled(t *testing.T) {
	mc := metrics.NewMemoryCollector()
	mw := metrics.New(mc, metrics.Config{EnableMethodLabels: false})
	inv := mw.Middleware()(successInvoker())
	input := &mockTLObject{id: tg.HelpGetConfigTypeID}

	_, _ = inv.RPCInvoke(context.Background(), input, nil)

	if got := mc.Requests("help.getConfig", metrics.StatusSuccess); got != 0 {
		t.Errorf("requests[help.getConfig] = %d, want 0 (labels disabled)", got)
	}
	if got := mc.Requests("unknown", metrics.StatusSuccess); got != 1 {
		t.Errorf("requests[unknown] = %d, want 1 (labels disabled)", got)
	}
}

func TestMethodName(t *testing.T) {
	mc := metrics.NewMemoryCollector()
	mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})
	inv := mw.Middleware()(successInvoker())
	input := &mockTLObject{id: tg.HelpGetConfigTypeID}

	_, _ = inv.RPCInvoke(context.Background(), input, nil)

	if got := mc.Requests("help.getConfig", metrics.StatusSuccess); got != 1 {
		t.Errorf("method name should resolve to 'help.getConfig', got %d", got)
	}
}

func TestErrorPassthrough(t *testing.T) {
	mc := metrics.NewMemoryCollector()
	mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})
	rpcErr := errors.New("USER_PRIVACY_RESTRICTED")
	base := &mockInvoker{
		fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			return nil, rpcErr
		},
	}
	inv := mw.Middleware()(base)

	_, err := inv.RPCInvoke(context.Background(), nil, nil)
	if err != rpcErr {
		t.Fatalf("error should be returned unchanged, got: %v", err)
	}
}

func TestResultPassthrough(t *testing.T) {
	mc := metrics.NewMemoryCollector()
	mw := metrics.New(mc, metrics.Config{EnableMethodLabels: true})
	result := &mockTLObject{id: 0x12345678}
	base := &mockInvoker{
		fn: func(_ context.Context, _ tg.TLObject, _ func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
			return result, nil
		},
	}
	inv := mw.Middleware()(base)

	got, err := inv.RPCInvoke(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != result {
		t.Fatal("result should be returned unchanged")
	}
}

// --- Prometheus collector test ---

func TestPrometheusCollector(t *testing.T) {
	pc := metrics.NewPrometheusCollector("mtgo")
	var _ metrics.Collector = pc

	pc.IncRequests("help.getConfig", metrics.StatusSuccess)
	pc.IncRequests("help.getConfig", metrics.StatusError)
	pc.ObserveLatency("help.getConfig", 42*time.Millisecond)
	pc.IncInFlight("help.getConfig")
	pc.DecInFlight("help.getConfig")
	pc.IncFloodWait("help.getConfig")
	pc.IncTimeout("help.getConfig")
	pc.IncRetry("help.getConfig")

	for _, c := range pc.Collectors() {
		if c == nil {
			t.Fatal("nil prometheus collector")
		}
	}
}
