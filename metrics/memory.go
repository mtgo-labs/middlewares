package metrics

import (
	"slices"
	"sync"
	"time"
)

type requestKey struct {
	method string
	status string
}

// MemoryCollector is an in-memory Collector for tests and simple use cases.
// It stores counters and latency samples in maps protected by a mutex.
type MemoryCollector struct {
	mu         sync.RWMutex
	requests   map[requestKey]int64
	latencies  map[string][]time.Duration
	inFlight   map[string]int64
	floodWaits map[string]int64
	timeouts   map[string]int64
	retries    map[string]int64
}

// NewMemoryCollector creates a ready-to-use in-memory collector.
func NewMemoryCollector() *MemoryCollector {
	return &MemoryCollector{
		requests:   make(map[requestKey]int64),
		latencies:  make(map[string][]time.Duration),
		inFlight:   make(map[string]int64),
		floodWaits: make(map[string]int64),
		timeouts:   make(map[string]int64),
		retries:    make(map[string]int64),
	}
}

func (m *MemoryCollector) IncRequests(method, status string) {
	m.mu.Lock()
	m.requests[requestKey{method, status}]++
	m.mu.Unlock()
}

func (m *MemoryCollector) ObserveLatency(method string, d time.Duration) {
	m.mu.Lock()
	m.latencies[method] = append(m.latencies[method], d)
	m.mu.Unlock()
}

func (m *MemoryCollector) IncInFlight(method string) {
	m.mu.Lock()
	m.inFlight[method]++
	m.mu.Unlock()
}

func (m *MemoryCollector) DecInFlight(method string) {
	m.mu.Lock()
	m.inFlight[method]--
	m.mu.Unlock()
}

func (m *MemoryCollector) IncFloodWait(method string) {
	m.mu.Lock()
	m.floodWaits[method]++
	m.mu.Unlock()
}

func (m *MemoryCollector) IncTimeout(method string) {
	m.mu.Lock()
	m.timeouts[method]++
	m.mu.Unlock()
}

func (m *MemoryCollector) IncRetry(method string) {
	m.mu.Lock()
	m.retries[method]++
	m.mu.Unlock()
}

// --- Read accessors (for tests and custom exposition) ---

// Requests returns the count of RPC requests for the given method and status.
func (m *MemoryCollector) Requests(method, status string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.requests[requestKey{method, status}]
}

// Latencies returns a copy of the recorded latency samples for the given method.
func (m *MemoryCollector) Latencies(method string) []time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Clone(m.latencies[method])
}

// InFlight returns the current in-flight count for the given method.
func (m *MemoryCollector) InFlight(method string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inFlight[method]
}

// FloodWaits returns the flood-wait count for the given method.
func (m *MemoryCollector) FloodWaits(method string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.floodWaits[method]
}

// Timeouts returns the timeout/cancelled count for the given method.
func (m *MemoryCollector) Timeouts(method string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.timeouts[method]
}

// Retries returns the retry count for the given method.
func (m *MemoryCollector) Retries(method string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.retries[method]
}
