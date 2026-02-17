package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	FramesProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "fd",
		Name:      "frames_processed_total",
		Help:      "Total number of frames processed",
	}, []string{"stream_id"})

	FacesDetected = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "fd",
		Name:      "faces_detected_total",
		Help:      "Total number of faces detected",
	}, []string{"stream_id"})

	FacesRecognized = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "fd",
		Name:      "faces_recognized_total",
		Help:      "Total number of faces recognized from database",
	}, []string{"stream_id"})

	InferenceDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "fd",
		Name:      "inference_duration_seconds",
		Help:      "Duration of ML inference stages",
		Buckets:   prometheus.ExponentialBuckets(0.005, 2, 10),
	}, []string{"stage"})

	QueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "fd",
		Name:      "queue_depth",
		Help:      "Number of pending frame tasks in queue",
	})

	ActiveStreams = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "fd",
		Name:      "active_streams",
		Help:      "Number of currently active video streams",
	})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "fd",
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request duration",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "path", "status"})

	WSConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "fd",
		Name:      "ws_connections",
		Help:      "Number of active WebSocket connections",
	})
)
