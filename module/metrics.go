package upload

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	ActiveUploads       *prometheus.GaugeVec
	Accepted            *prometheus.CounterVec
	Completed           *prometheus.CounterVec
	Failed              *prometheus.CounterVec
	Cancelled           *prometheus.CounterVec
	RejectedTokens      *prometheus.CounterVec
	ExpiredTokens       *prometheus.CounterVec
	SizeLimitFailures   *prometheus.CounterVec
	ContentTypeFailures *prometheus.CounterVec
	BytesReceived       *prometheus.CounterVec
	BackendFailures     *prometheus.CounterVec
	WorkerFailures      *prometheus.CounterVec
	Config              *prometheus.GaugeVec
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		ActiveUploads: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "pogo_upload",
			Name:      "active_uploads",
			Help:      "Current number of in-flight uploads",
		}, []string{"store"}),
		Accepted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pogo_upload",
			Name:      "accepted_total",
			Help:      "Total uploads accepted after token and request validation",
		}, []string{"store"}),
		Completed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pogo_upload",
			Name:      "completed_total",
			Help:      "Total uploads completed and acknowledged by the PHP worker",
		}, []string{"store"}),
		Failed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pogo_upload",
			Name:      "failed_total",
			Help:      "Total uploads failed after acceptance",
		}, []string{"store"}),
		Cancelled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pogo_upload",
			Name:      "cancelled_total",
			Help:      "Total process-local upload cancellations",
		}, []string{"store"}),
		RejectedTokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pogo_upload",
			Name:      "rejected_tokens_total",
			Help:      "Total malformed or invalid upload tokens",
		}, []string{"store"}),
		ExpiredTokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pogo_upload",
			Name:      "expired_tokens_total",
			Help:      "Total expired upload tokens",
		}, []string{"store"}),
		SizeLimitFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pogo_upload",
			Name:      "size_limit_failures_total",
			Help:      "Total uploads rejected or failed because of byte limits",
		}, []string{"store"}),
		ContentTypeFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pogo_upload",
			Name:      "content_type_failures_total",
			Help:      "Total uploads rejected because of Content-Type constraints",
		}, []string{"store"}),
		BytesReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pogo_upload",
			Name:      "bytes_received_total",
			Help:      "Total upload body bytes read",
		}, []string{"store"}),
		BackendFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pogo_upload",
			Name:      "backend_write_failures_total",
			Help:      "Total backend write or commit failures",
		}, []string{"store"}),
		WorkerFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "pogo_upload",
			Name:      "worker_event_failures_total",
			Help:      "Total PHP worker event failures",
		}, []string{"store"}),
		Config: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "pogo_upload",
			Name:      "config",
			Help:      "Effective Pogo Upload store configuration by key",
		}, []string{"store", "key"}),
	}

	if reg != nil {
		m.ActiveUploads = registerGaugeVec(reg, m.ActiveUploads)
		m.Accepted = registerCounterVec(reg, m.Accepted)
		m.Completed = registerCounterVec(reg, m.Completed)
		m.Failed = registerCounterVec(reg, m.Failed)
		m.Cancelled = registerCounterVec(reg, m.Cancelled)
		m.RejectedTokens = registerCounterVec(reg, m.RejectedTokens)
		m.ExpiredTokens = registerCounterVec(reg, m.ExpiredTokens)
		m.SizeLimitFailures = registerCounterVec(reg, m.SizeLimitFailures)
		m.ContentTypeFailures = registerCounterVec(reg, m.ContentTypeFailures)
		m.BytesReceived = registerCounterVec(reg, m.BytesReceived)
		m.BackendFailures = registerCounterVec(reg, m.BackendFailures)
		m.WorkerFailures = registerCounterVec(reg, m.WorkerFailures)
		m.Config = registerGaugeVec(reg, m.Config)
	}

	return m
}

func registerCounterVec(reg prometheus.Registerer, collector *prometheus.CounterVec) *prometheus.CounterVec {
	if err := reg.Register(collector); err != nil {
		if already, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := already.ExistingCollector.(*prometheus.CounterVec); ok {
				return existing
			}
		}
	}
	return collector
}

func registerGaugeVec(reg prometheus.Registerer, collector *prometheus.GaugeVec) *prometheus.GaugeVec {
	if err := reg.Register(collector); err != nil {
		if already, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := already.ExistingCollector.(*prometheus.GaugeVec); ok {
				return existing
			}
		}
	}
	return collector
}

func (m *Metrics) SetConfig(store string, cfg configMetricsSnapshot) {
	if m == nil || m.Config == nil {
		return
	}
	m.Config.WithLabelValues(store, "token_ttl_seconds").Set(float64(cfg.TokenTTLSeconds))
	m.Config.WithLabelValues(store, "max_upload_bytes").Set(float64(cfg.MaxUploadBytes))
	m.Config.WithLabelValues(store, "max_concurrency").Set(float64(cfg.MaxConcurrency))
	m.Config.WithLabelValues(store, "read_timeout_seconds").Set(float64(cfg.ReadTimeoutSeconds))
	m.Config.WithLabelValues(store, "complete_timeout_seconds").Set(float64(cfg.CompleteTimeoutSeconds))
	m.Config.WithLabelValues(store, "progress_ttl_seconds").Set(float64(cfg.ProgressTTLSeconds))
}

func (m *Metrics) SetActive(store string, value int) {
	if m != nil && m.ActiveUploads != nil {
		m.ActiveUploads.WithLabelValues(store).Set(float64(value))
	}
}

func (m *Metrics) IncAccepted(store string) {
	if m != nil && m.Accepted != nil {
		m.Accepted.WithLabelValues(store).Inc()
	}
}

func (m *Metrics) IncCompleted(store string) {
	if m != nil && m.Completed != nil {
		m.Completed.WithLabelValues(store).Inc()
	}
}

func (m *Metrics) IncFailed(store string) {
	if m != nil && m.Failed != nil {
		m.Failed.WithLabelValues(store).Inc()
	}
}

func (m *Metrics) IncCancelled(store string) {
	if m != nil && m.Cancelled != nil {
		m.Cancelled.WithLabelValues(store).Inc()
	}
}

func (m *Metrics) IncRejectedToken(store string) {
	if m != nil && m.RejectedTokens != nil {
		m.RejectedTokens.WithLabelValues(store).Inc()
	}
}

func (m *Metrics) IncExpiredToken(store string) {
	if m != nil && m.ExpiredTokens != nil {
		m.ExpiredTokens.WithLabelValues(store).Inc()
	}
}

func (m *Metrics) IncSizeLimit(store string) {
	if m != nil && m.SizeLimitFailures != nil {
		m.SizeLimitFailures.WithLabelValues(store).Inc()
	}
}

func (m *Metrics) IncContentTypeFailure(store string) {
	if m != nil && m.ContentTypeFailures != nil {
		m.ContentTypeFailures.WithLabelValues(store).Inc()
	}
}

func (m *Metrics) AddBytes(store string, bytes int64) {
	if m != nil && m.BytesReceived != nil && bytes > 0 {
		m.BytesReceived.WithLabelValues(store).Add(float64(bytes))
	}
}

func (m *Metrics) IncBackendFailure(store string) {
	if m != nil && m.BackendFailures != nil {
		m.BackendFailures.WithLabelValues(store).Inc()
	}
}

func (m *Metrics) IncWorkerFailure(store string) {
	if m != nil && m.WorkerFailures != nil {
		m.WorkerFailures.WithLabelValues(store).Inc()
	}
}
