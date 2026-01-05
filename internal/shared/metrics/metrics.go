package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Control Plane メトリクス
	JobsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flyencoder_jobs_total",
			Help: "Total number of encoding jobs",
		},
		[]string{"status"}, // completed, failed
	)

	JobDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flyencoder_job_duration_seconds",
			Help:    "Job duration in seconds",
			Buckets: []float64{10, 30, 60, 120, 300, 600, 1200, 1800, 3600},
		},
		[]string{"preset"},
	)

	// Worker メトリクス
	ActiveJobs = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flyencoder_worker_active_jobs",
			Help: "Number of currently active jobs on worker",
		},
		[]string{"worker_id"},
	)

	EncodingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flyencoder_encoding_duration_seconds",
			Help:    "Encoding duration in seconds",
			Buckets: []float64{10, 30, 60, 120, 300, 600, 1200, 1800, 3600},
		},
		[]string{"preset", "worker_id"},
	)

	UploadDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flyencoder_upload_duration_seconds",
			Help:    "Upload duration in seconds",
			Buckets: []float64{1, 5, 10, 30, 60, 120, 300},
		},
		[]string{"storage_type", "worker_id"},
	)

	UploadSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flyencoder_upload_size_bytes",
			Help:    "Upload file size in bytes",
			Buckets: prometheus.ExponentialBuckets(1024*1024, 2, 12), // 1MB to 4GB
		},
		[]string{"storage_type", "worker_id"},
	)
)
