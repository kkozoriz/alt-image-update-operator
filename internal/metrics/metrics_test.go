package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestRegisterAddsBuildAndRolloutMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()

	if err := Register(registry); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	DefaultRecorder.RecordBuild(ResultSucceeded)
	DefaultRecorder.RecordBuild(ResultFailed)
	DefaultRecorder.RecordRollout(ResultSucceeded)
	DefaultRecorder.RecordRollout(ResultFailed)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather returned error: %v", err)
	}

	assertCounterMetric(t, families, BuildTotalMetricName, string(ResultSucceeded))
	assertCounterMetric(t, families, BuildTotalMetricName, string(ResultFailed))
	assertCounterMetric(t, families, RolloutTotalMetricName, string(ResultSucceeded))
	assertCounterMetric(t, families, RolloutTotalMetricName, string(ResultFailed))
}

func TestRegisterCanBeCalledRepeatedly(t *testing.T) {
	registry := prometheus.NewRegistry()

	if err := Register(registry); err != nil {
		t.Fatalf("first Register returned error: %v", err)
	}
	if err := Register(registry); err != nil {
		t.Fatalf("second Register returned error: %v", err)
	}
}

func TestRegisterRejectsNilRegistry(t *testing.T) {
	if err := Register(nil); err == nil {
		t.Fatal("Register(nil) error = nil, want error")
	}
}

func assertCounterMetric(t *testing.T, families []*dto.MetricFamily, name, result string) {
	t.Helper()

	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metric.GetCounter() == nil {
				t.Fatalf("%s has non-counter metric %#v", name, metric)
			}
			if metric.GetCounter().GetValue() <= 0 {
				t.Fatalf("%s{%q} value = %f, want > 0", name, result, metric.GetCounter().GetValue())
			}
			if hasOnlyResultLabel(metric, result) {
				return
			}
		}
		t.Fatalf("%s missing result label %q", name, result)
	}
	t.Fatalf("missing metric family %q", name)
}

func hasOnlyResultLabel(metric *dto.Metric, result string) bool {
	if len(metric.GetLabel()) != 1 {
		return false
	}
	label := metric.GetLabel()[0]
	return label.GetName() == "result" && label.GetValue() == result
}
