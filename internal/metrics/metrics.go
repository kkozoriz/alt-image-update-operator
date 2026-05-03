package metrics

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	BuildTotalMetricName   = "alt_image_update_build_total"
	RolloutTotalMetricName = "alt_image_update_rollout_total"

	ResultSucceeded Result = "succeeded"
	ResultFailed    Result = "failed"
)

type Result string

// Recorder records low-cardinality controller outcome metrics.
type Recorder interface {
	RecordBuild(result Result)
	RecordRollout(result Result)
}

type prometheusRecorder struct {
	buildTotal   *prometheus.CounterVec
	rolloutTotal *prometheus.CounterVec
}

var (
	buildTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: BuildTotalMetricName,
			Help: "Total number of AltImageUpdatePolicy build outcomes.",
		},
		[]string{"result"},
	)
	rolloutTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: RolloutTotalMetricName,
			Help: "Total number of AltImageUpdatePolicy rollout outcomes.",
		},
		[]string{"result"},
	)

	DefaultRecorder Recorder = prometheusRecorder{
		buildTotal:   buildTotal,
		rolloutTotal: rolloutTotal,
	}
)

func (r prometheusRecorder) RecordBuild(result Result) {
	r.buildTotal.WithLabelValues(string(result)).Inc()
}

func (r prometheusRecorder) RecordRollout(result Result) {
	r.rolloutTotal.WithLabelValues(string(result)).Inc()
}

// Register adds the controller metrics to the supplied registry.
func Register(registerer prometheus.Registerer) error {
	if registerer == nil {
		return errors.New("metrics registerer is nil")
	}
	if err := registerCollector(registerer, buildTotal); err != nil {
		return err
	}
	return registerCollector(registerer, rolloutTotal)
}

func registerCollector(registerer prometheus.Registerer, collector prometheus.Collector) error {
	if err := registerer.Register(collector); err != nil {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegistered) {
			return nil
		}
		return err
	}
	return nil
}
