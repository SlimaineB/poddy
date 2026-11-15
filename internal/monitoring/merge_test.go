package monitoring

import (
	"testing"
	"time"
)

func TestMergeHistoriesSumsSamplesByBucket(t *testing.T) {
	start := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	first := History{
		Available:   true,
		Source:      "prometheus",
		Start:       start,
		End:         start.Add(2 * time.Minute),
		StepSeconds: 60,
		Series: []Series{{
			Key: "cpu", Label: "CPU", Unit: "cores",
			Points: []Point{{Timestamp: float64(start.Unix()), Value: 1}, {Timestamp: float64(start.Add(time.Minute).Unix()), Value: 2}},
		}},
	}
	second := History{
		Available:   true,
		Source:      "prometheus",
		Start:       start,
		End:         start.Add(2 * time.Minute),
		StepSeconds: 60,
		Series: []Series{{
			Key: "cpu", Label: "CPU", Unit: "cores",
			Points: []Point{{Timestamp: float64(start.Unix()), Value: 3}, {Timestamp: float64(start.Add(time.Minute).Unix()), Value: 4}},
		}},
	}

	merged := MergeHistories([]History{first, second})
	if !merged.Available || len(merged.Series) != 1 {
		t.Fatalf("merged = %+v", merged)
	}
	points := merged.Series[0].Points
	if len(points) != 2 {
		t.Fatalf("points = %+v", points)
	}
	if points[0].Value != 4 || points[1].Value != 6 {
		t.Fatalf("points = %+v", points)
	}
}

func TestMergeHistoriesKeepsUnavailableReason(t *testing.T) {
	merged := MergeHistories([]History{{Available: false, Reason: "Prometheus is unavailable"}})
	if merged.Available {
		t.Fatal("available = true")
	}
	if merged.Reason != "Prometheus is unavailable" {
		t.Fatalf("reason = %q", merged.Reason)
	}
	if len(merged.Series) != 0 {
		t.Fatalf("series = %+v", merged.Series)
	}
}

func TestMergeHistoriesEmpty(t *testing.T) {
	merged := MergeHistories(nil)
	if merged.Available || len(merged.Series) != 0 {
		t.Fatalf("merged = %+v", merged)
	}
}
