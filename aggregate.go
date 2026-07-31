// aggregate.go

package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
)

// sampleLevel is a -sample flag value: how finely to bucket readings.
type sampleLevel string

const (
	sampleRaw     sampleLevel = "raw"
	sampleHour    sampleLevel = "hour"
	sampleDay     sampleLevel = "day"
	sampleWeek    sampleLevel = "week"
	sampleMonth   sampleLevel = "month"
	sampleQuarter sampleLevel = "quarter"
)

func parseSampleLevel(s string) (sampleLevel, error) {
	switch sampleLevel(s) {
	case sampleRaw, sampleHour, sampleDay, sampleWeek, sampleMonth, sampleQuarter:
		return sampleLevel(s), nil
	default:
		return "", fmt.Errorf("must be one of raw, hour, day, week, month, quarter")
	}
}

// viennaLocation is used for day/week/month/quarter bucket boundaries, so
// a "day" total starts at Vienna-local midnight rather than UTC midnight.
// hour buckets don't need it (an hour is the same duration in any zone).
//
// ponytail: timezone hardcoded to Europe/Vienna; add a -tz flag if this
// tool ever serves a non-AT account.
var viennaLocation = mustLoadLocation("Europe/Vienna")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// bucketStart returns the start of the level-sized bucket containing t.
func bucketStart(t time.Time, level sampleLevel) time.Time {
	switch level {
	case sampleHour:
		return t.Truncate(time.Hour)
	case sampleDay:
		local := t.In(viennaLocation)
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, viennaLocation)
	case sampleWeek:
		local := t.In(viennaLocation)
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, viennaLocation)
		offset := (int(day.Weekday()) + 6) % 7 // Monday-start: Mon=0 ... Sun=6
		return day.AddDate(0, 0, -offset)
	case sampleMonth:
		local := t.In(viennaLocation)
		return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, viennaLocation)
	case sampleQuarter:
		local := t.In(viennaLocation)
		firstMonthOfQuarter := time.Month(((int(local.Month())-1)/3)*3 + 1)
		return time.Date(local.Year(), firstMonthOfQuarter, 1, 0, 0, 0, 0, viennaLocation)
	default: // sampleRaw, or unrecognized — caller validates level via parseSampleLevel
		return t
	}
}

// qualityRank orders quality codes from most to least trusted, so
// aggregate can propagate the worst one across a bucket's readings.
func qualityRank(q string) int {
	switch q {
	case "L3":
		return 2
	case "L2":
		return 1
	default:
		return 0
	}
}

// aggregate groups readings into level-sized buckets and sums each
// bucket's Value, propagating the worst (least trusted) Quality among its
// readings. level == sampleRaw returns readings unchanged. readings must
// already be sorted ascending by Timestamp (as store.Get returns them);
// output is sorted ascending by bucket start.
func aggregate(readings []provider.Reading, level sampleLevel) []provider.Reading {
	if level == sampleRaw {
		return readings
	}

	type bucket struct {
		value   float64
		quality string
	}
	buckets := make(map[time.Time]*bucket)
	var order []time.Time
	for _, r := range readings {
		start := bucketStart(r.Timestamp, level)
		b, ok := buckets[start]
		if !ok {
			b = &bucket{}
			buckets[start] = b
			order = append(order, start)
		}
		b.value += r.Value
		if qualityRank(r.Quality) > qualityRank(b.quality) {
			b.quality = r.Quality
		}
	}

	sort.Slice(order, func(i, j int) bool { return order[i].Before(order[j]) })
	out := make([]provider.Reading, len(order))
	for i, start := range order {
		b := buckets[start]
		out[i] = provider.Reading{Timestamp: start.UTC(), Value: b.value, Quality: b.quality}
	}
	return out
}
