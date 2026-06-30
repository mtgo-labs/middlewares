// Package timeout provides invoker middleware that applies context-based
// deadlines to outgoing RPC calls, preventing requests from hanging forever.
//
// The middleware wraps every outgoing RPC call with [context.WithTimeout],
// respecting caller-provided deadlines (it never extends them). When the
// configured timeout fires, the RPC context is cancelled and a typed
// [*TimeoutError] is returned.
//
// Example:
//
//	// Apply a 30s default to all RPC calls.
//	client.UseInvokerMiddleware(timeout.Timeout(30 * time.Second))
//
//	// Or with per-method overrides and exclusions.
//	client.UseInvokerMiddleware(timeout.TimeoutConfig(timeout.TimeoutOptions{
//	    Default: 30 * time.Second,
//	    Methods: map[string]time.Duration{
//	        "messages.sendMessage": 10 * time.Second,
//	        "upload.saveFilePart":  2 * time.Minute,
//	    },
//	    DisabledMethods: []string{"updates.getDifference"},
//	}))
package timeout

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mtgo-labs/mtgo/tg"
	"github.com/mtgo-labs/mtgo/tgerr"
)

// ErrTimeout is the sentinel error returned when an RPC call exceeds its
// configured timeout. Use errors.Is(err, ErrTimeout) to detect middleware
// timeouts.
//
// [*TimeoutError] also unwraps to [context.DeadlineExceeded], so existing code
// that checks for deadline-exceeded continues to work.
var ErrTimeout = errors.New("timeout: RPC exceeded configured duration")

// TimeoutError provides details about a timed-out RPC call.
//
// It satisfies:
//   - errors.Is(err, ErrTimeout)
//   - errors.Is(err, context.DeadlineExceeded)
type TimeoutError struct {
	Method  string        // The Telegram method name (e.g. "help.getConfig").
	Timeout time.Duration // The configured timeout that was exceeded.
	err     error         // Underlying error (always context.DeadlineExceeded).
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("timeout: RPC %q exceeded %s", e.Method, e.Timeout)
}

func (e *TimeoutError) Unwrap() error { return e.err }

// Is reports whether the error matches ErrTimeout. The DeadlineExceeded match
// is handled by Unwrap so existing code that checks for deadline-exceeded
// continues to work.
func (e *TimeoutError) Is(target error) bool {
	return errors.Is(target, ErrTimeout)
}

// TimeoutOptions configures the timeout middleware.
type TimeoutOptions struct {
	// Default is the timeout applied to all RPC calls that do not have a
	// per-method override or are not in DisabledMethods.
	// Zero or negative means no default timeout.
	Default time.Duration

	// Methods sets per-method timeout overrides. The key is the Telegram
	// method name (e.g. "messages.sendMessage"). Entries here take
	// precedence over Default.
	Methods map[string]time.Duration

	// DisabledMethods disables the timeout for specific methods, letting
	// them run indefinitely (useful for long-poll methods like
	// updates.getDifference). Takes precedence over Methods and Default.
	DisabledMethods []string
}

// Timeout returns an invoker middleware that applies the given timeout to
// every outgoing RPC call.
//
// It is shorthand for TimeoutConfig(TimeoutOptions{Default: d}).
func Timeout(d time.Duration) func(next tg.Invoker) tg.Invoker {
	return TimeoutConfig(TimeoutOptions{Default: d})
}

// TimeoutConfig returns an invoker middleware configured by opts.
//
// The returned function is suitable for [tg.Client.UseInvokerMiddleware].
func TimeoutConfig(opts TimeoutOptions) func(next tg.Invoker) tg.Invoker {
	// Copy maps so caller mutations after registration don't affect the
	// middleware. Read-only maps are safe for concurrent access.
	methods := make(map[string]time.Duration, len(opts.Methods))
	for k, v := range opts.Methods {
		methods[k] = v
	}
	disabled := make(map[string]struct{}, len(opts.DisabledMethods))
	for _, m := range opts.DisabledMethods {
		disabled[m] = struct{}{}
	}

	return func(next tg.Invoker) tg.Invoker {
		return &invoker{
			defaultTimeout: opts.Default,
			methods:        methods,
			disabled:       disabled,
			next:           next,
		}
	}
}

// invoker implements [tg.Invoker], wrapping RPC calls with context deadlines.
// It holds only immutable configuration set at construction time and is safe
// for concurrent use without locks.
type invoker struct {
	defaultTimeout time.Duration
	methods        map[string]time.Duration
	disabled       map[string]struct{}
	next           tg.Invoker
}

func (i *invoker) RPCInvoke(ctx context.Context, input tg.TLObject, decode func(*tg.Reader) (tg.TLObject, error)) (tg.TLObject, error) {
	d, ok := i.resolveTimeout(input)
	if !ok {
		return i.next.RPCInvoke(ctx, input, decode)
	}

	derived, cancel, ours := applyDeadline(ctx, d)
	defer cancel()

	result, err := i.next.RPCInvoke(derived, input, decode)
	if err != nil && ours && isTimeoutErr(err) {
		return result, &TimeoutError{Method: methodName(input), Timeout: d, err: context.DeadlineExceeded}
	}
	return result, err
}

func (i *invoker) RPCInvokeRaw(ctx context.Context, input tg.TLObject) ([]byte, error) {
	d, ok := i.resolveTimeout(input)
	if !ok {
		return i.next.RPCInvokeRaw(ctx, input)
	}

	derived, cancel, ours := applyDeadline(ctx, d)
	defer cancel()

	result, err := i.next.RPCInvokeRaw(derived, input)
	if err != nil && ours && isTimeoutErr(err) {
		return result, &TimeoutError{Method: methodName(input), Timeout: d, err: context.DeadlineExceeded}
	}
	return result, err
}

// resolveTimeout returns the effective timeout for the given RPC input and
// whether a timeout should be applied. The method name is resolved internally
// only when per-method configuration exists; the common default-only path
// skips the reverse NamesMap lookup entirely.
func (i *invoker) resolveTimeout(input tg.TLObject) (time.Duration, bool) {
	if len(i.methods) == 0 && len(i.disabled) == 0 {
		if i.defaultTimeout > 0 {
			return i.defaultTimeout, true
		}
		return 0, false
	}

	method := methodName(input)

	if _, off := i.disabled[method]; off {
		return 0, false
	}
	if d, ok := i.methods[method]; ok {
		return d, true
	}
	if i.defaultTimeout > 0 {
		return i.defaultTimeout, true
	}
	return 0, false
}

// applyDeadline derives a context with the middleware's timeout applied.
//
// If the caller's context already has an earlier deadline, it is preserved —
// the returned context is unchanged and ours is false so the caller's
// DeadlineExceeded error passes through without wrapping.
//
// The returned cancel function must always be called to avoid leaking the
// internal timer.
func applyDeadline(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc, bool) {
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		// Caller deadline is sooner — let it govern.
		return ctx, func() {}, false
	}
	derived, cancel := context.WithTimeout(ctx, timeout)
	return derived, cancel, true
}

// isTimeoutErr reports whether err represents a timeout (not a FLOOD_WAIT).
// FloodWait errors are explicitly excluded — those are server-side rate limits,
// not local timeouts.
func isTimeoutErr(err error) bool {
	if _, ok := tgerr.AsFloodWait(err); ok {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded)
}

var (
	nameMapOnce sync.Once
	idToName    map[uint32]string
)

func initNameMap() {
	idToName = make(map[uint32]string, len(tg.NamesMap))
	for name, id := range tg.NamesMap {
		idToName[id] = name
	}
}

// methodName resolves the Telegram method name for a TLObject via reverse
// lookup of tg.NamesMap. Returns "unknown" for nil input or unrecognised IDs.
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
