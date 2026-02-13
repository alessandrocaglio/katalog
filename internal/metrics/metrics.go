package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	LinesProcessed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "log_forwarder_lines_total",
			Help: "Total number of log lines processed per file",
		},
		[]string{"path", "group"},
	)
	FileErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "log_forwarder_file_errors_total",
			Help: "Total number of file errors",
		},
		[]string{"path", "error_type"},
	)
)

func Init() {
	prometheus.MustRegister(LinesProcessed, FileErrors)
}
