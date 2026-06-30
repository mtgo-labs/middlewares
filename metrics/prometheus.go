package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// latencyBuckets are optimised for RPC round-trip times (1 ms to 10 s).
var latencyBuckets = []float64{
	0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// PrometheusCollector implements [Collector] using Prometheus metrics.
//
// Metrics are NOT automatically registered with the default Prometheus
// registry. Register them explicitly:
//
//	pc := metrics.NewPrometheusCollector("mtgo")
//	prometheus.MustRegister(pc.Collectors()...)
type PrometheusCollector struct {
	requests  *prometheus.CounterVec
	latency   *prometheus.HistogramVec
	inFlight  *prometheus.GaugeVec
	floodWait *prometheus.CounterVec
	timeout   *prometheus.CounterVec
	retry     *prometheus.CounterVec
}

// NewPrometheusCollector creates a Prometheus-backed collector.
// namespace is prepended to all metric names (e.g. "mtgo" produces
// "mtgo_rpc_requests_total").
func NewPrometheusCollector(namespace string) *PrometheusCollector {
	return &PrometheusCollector{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "rpc_requests_total",
			Help:      "Total number of RPC requests by method and status.",
		}, []string{"method", "status"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "rpc_latency_seconds",
			Help:      "RPC round-trip latency in seconds by method.",
			Buckets:   latencyBuckets,
		}, []string{"method"}),
		inFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "rpc_in_flight",
			Help:      "Number of in-flight RPC requests by method.",
		}, []string{"method"}),
		floodWait: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "rpc_flood_wait_total",
			Help:      "Total number of FLOOD_WAIT errors by method.",
		}, []string{"method"}),
		timeout: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "rpc_timeout_total",
			Help:      "Total number of timed-out or cancelled RPC requests by method.",
		}, []string{"method"}),
		retry: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "rpc_retries_total",
			Help:      "Total number of RPC retries by method.",
		}, []string{"method"}),
	}
}

func (p *PrometheusCollector) IncRequests(method, status string) {
	p.requests.WithLabelValues(method, status).Inc()
}

func (p *PrometheusCollector) ObserveLatency(method string, d time.Duration) {
	p.latency.WithLabelValues(method).Observe(d.Seconds())
}

func (p *PrometheusCollector) IncInFlight(method string) {
	p.inFlight.WithLabelValues(method).Inc()
}

func (p *PrometheusCollector) DecInFlight(method string) {
	p.inFlight.WithLabelValues(method).Dec()
}

func (p *PrometheusCollector) IncFloodWait(method string) {
	p.floodWait.WithLabelValues(method).Inc()
}

func (p *PrometheusCollector) IncTimeout(method string) {
	p.timeout.WithLabelValues(method).Inc()
}

func (p *PrometheusCollector) IncRetry(method string) {
	p.retry.WithLabelValues(method).Inc()
}

// Collectors returns the underlying Prometheus collectors for manual
// registration with a [prometheus.Registerer].
func (p *PrometheusCollector) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		p.requests,
		p.latency,
		p.inFlight,
		p.floodWait,
		p.timeout,
		p.retry,
	}
}
