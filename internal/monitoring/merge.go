package monitoring

import (
	"math"
	"sort"
)

// MergeHistories combines history responses from several regions into one
// portal-wide history. Samples from different Prometheus instances are summed
// into buckets aligned on the smallest common step so peaks and averages stay
// meaningful without duplicating samples from each region.
func MergeHistories(histories []History) History {
	out := History{Series: []Series{}}
	if len(histories) == 0 {
		return out
	}

	step := int64(0)
	source := ""
	reason := ""
	raw := map[string][]Point{}
	order := make([]string, 0)
	labels := map[string]Series{}
	minTimestamp := math.Inf(1)

	for _, history := range histories {
		if history.Available {
			out.Available = true
		}
		if source == "" && history.Source != "" {
			source = history.Source
		}
		if reason == "" && history.Reason != "" {
			reason = history.Reason
		}
		if history.StepSeconds > 0 && (step == 0 || history.StepSeconds < step) {
			step = history.StepSeconds
		}
		if !history.Start.IsZero() && (out.Start.IsZero() || history.Start.Before(out.Start)) {
			out.Start = history.Start
		}
		if history.End.After(out.End) {
			out.End = history.End
		}
		for _, series := range history.Series {
			if _, ok := raw[series.Key]; !ok {
				order = append(order, series.Key)
				labels[series.Key] = series
			}
			for _, point := range series.Points {
				if math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
					continue
				}
				raw[series.Key] = append(raw[series.Key], point)
				if point.Timestamp < minTimestamp {
					minTimestamp = point.Timestamp
				}
			}
		}
	}

	if step <= 0 {
		step = 60
	}
	out.StepSeconds = step
	out.Source = source
	if !out.Available {
		out.Reason = reason
		return out
	}

	for _, key := range order {
		points := raw[key]
		if len(points) == 0 {
			continue
		}
		buckets := map[int64]float64{}
		for _, point := range points {
			bucket := int64(math.Round((point.Timestamp - minTimestamp) / float64(step)))
			buckets[bucket] += point.Value
		}
		indexes := make([]int64, 0, len(buckets))
		for index := range buckets {
			indexes = append(indexes, index)
		}
		sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
		merged := make([]Point, 0, len(indexes))
		for _, index := range indexes {
			merged = append(merged, Point{
				Timestamp: minTimestamp + float64(index)*float64(step),
				Value:     buckets[index],
			})
		}
		series := labels[key]
		series.Points = merged
		out.Series = append(out.Series, series)
	}
	if len(out.Series) == 0 {
		out.Available = false
		out.Reason = reason
		if out.Reason == "" {
			out.Reason = "no historical series returned"
		}
	}
	return out
}
