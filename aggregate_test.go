// aggregate_test.go
package main

import (
	"testing"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
)

func mustParseRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("time.Parse(%q): %v", s, err)
	}
	return ts
}

func TestParseSampleLevel(t *testing.T) {
	for _, ok := range []string{"raw", "hour", "day", "week", "month", "quarter"} {
		if _, err := parseSampleLevel(ok); err != nil {
			t.Errorf("parseSampleLevel(%q): %v", ok, err)
		}
	}
	if _, err := parseSampleLevel("fortnight"); err == nil {
		t.Error("parseSampleLevel(fortnight) = nil error, want error")
	}
}

func TestAggregate_RawReturnsInputUnchanged(t *testing.T) {
	in := []provider.Reading{{Timestamp: mustParseRFC3339(t, "2024-01-15T00:00:00Z"), Value: 1}}
	out := aggregate(in, sampleRaw)
	if len(out) != 1 || out[0].Value != 1 {
		t.Errorf("aggregate(raw) = %+v, want input unchanged", out)
	}
}

func TestAggregate_HourSumsQuarterHourReadings(t *testing.T) {
	in := []provider.Reading{
		{Timestamp: mustParseRFC3339(t, "2024-01-15T10:00:00Z"), Value: 100},
		{Timestamp: mustParseRFC3339(t, "2024-01-15T10:15:00Z"), Value: 200},
		{Timestamp: mustParseRFC3339(t, "2024-01-15T10:30:00Z"), Value: 300},
		{Timestamp: mustParseRFC3339(t, "2024-01-15T10:45:00Z"), Value: 400},
		{Timestamp: mustParseRFC3339(t, "2024-01-15T11:00:00Z"), Value: 1},
	}
	out := aggregate(in, sampleHour)
	if len(out) != 2 {
		t.Fatalf("aggregate(hour) = %+v, want 2 buckets", out)
	}
	if out[0].Value != 1000 {
		t.Errorf("bucket[0].Value = %v, want 1000 (sum of the four 10:00 readings)", out[0].Value)
	}
	want := mustParseRFC3339(t, "2024-01-15T10:00:00Z")
	if !out[0].Timestamp.Equal(want) {
		t.Errorf("bucket[0].Timestamp = %v, want %v", out[0].Timestamp, want)
	}
	if out[1].Value != 1 {
		t.Errorf("bucket[1].Value = %v, want 1", out[1].Value)
	}
}

func TestAggregate_DayBucketsByViennaWallClockNotUTC(t *testing.T) {
	// 2024-06-15 is CEST (UTC+2). 22:15/22:30 UTC on the 14th is
	// 00:15/00:30 local time on the 15th — both must land in the
	// Vienna-local "2024-06-15" bucket, not the UTC-day "2024-06-14" one.
	in := []provider.Reading{
		{Timestamp: mustParseRFC3339(t, "2024-06-14T22:15:00Z"), Value: 10},
		{Timestamp: mustParseRFC3339(t, "2024-06-14T22:30:00Z"), Value: 20},
	}
	out := aggregate(in, sampleDay)
	if len(out) != 1 {
		t.Fatalf("aggregate(day) = %+v, want 1 bucket", out)
	}
	if out[0].Value != 30 {
		t.Errorf("bucket.Value = %v, want 30", out[0].Value)
	}
	// Vienna midnight on 2024-06-15 (CEST, UTC+2) is 2024-06-14T22:00:00Z.
	want := mustParseRFC3339(t, "2024-06-14T22:00:00Z")
	if !out[0].Timestamp.Equal(want) {
		t.Errorf("bucket.Timestamp = %v, want %v (Vienna-local day start)", out[0].Timestamp, want)
	}
}

func TestAggregate_WeekBucketsStartOnMonday(t *testing.T) {
	// 2024-01-17 is a Wednesday; the containing week (Vienna-local)
	// starts Monday 2024-01-15 00:00 CET = 2024-01-14T23:00:00Z.
	in := []provider.Reading{{Timestamp: mustParseRFC3339(t, "2024-01-17T10:00:00Z"), Value: 5}}
	out := aggregate(in, sampleWeek)
	want := mustParseRFC3339(t, "2024-01-14T23:00:00Z")
	if len(out) != 1 || !out[0].Timestamp.Equal(want) {
		t.Errorf("aggregate(week) = %+v, want single bucket starting %v", out, want)
	}
}

func TestAggregate_MonthAndQuarterBucketStarts(t *testing.T) {
	in := []provider.Reading{
		{Timestamp: mustParseRFC3339(t, "2024-02-10T10:00:00Z"), Value: 1},
		{Timestamp: mustParseRFC3339(t, "2024-05-10T10:00:00Z"), Value: 1},
	}
	month := aggregate(in, sampleMonth)
	if len(month) != 2 {
		t.Fatalf("aggregate(month) = %+v, want 2 buckets", month)
	}
	// Vienna midnight 2024-02-01 (CET, UTC+1) = 2024-01-31T23:00:00Z.
	wantFeb := mustParseRFC3339(t, "2024-01-31T23:00:00Z")
	if !month[0].Timestamp.Equal(wantFeb) {
		t.Errorf("month[0].Timestamp = %v, want %v", month[0].Timestamp, wantFeb)
	}

	quarter := aggregate(in, sampleQuarter)
	if len(quarter) != 2 {
		t.Fatalf("aggregate(quarter) = %+v, want 2 buckets (Feb is Q1, May is Q2)", quarter)
	}
}

func TestAggregate_QualityIsWorstOfConstituents(t *testing.T) {
	in := []provider.Reading{
		{Timestamp: mustParseRFC3339(t, "2024-01-15T10:00:00Z"), Value: 1, Quality: ""},
		{Timestamp: mustParseRFC3339(t, "2024-01-15T10:15:00Z"), Value: 1, Quality: "L2"},
		{Timestamp: mustParseRFC3339(t, "2024-01-15T10:30:00Z"), Value: 1, Quality: "L3"},
		{Timestamp: mustParseRFC3339(t, "2024-01-15T10:45:00Z"), Value: 1, Quality: "L2"},
	}
	out := aggregate(in, sampleHour)
	if len(out) != 1 || out[0].Quality != "L3" {
		t.Errorf("aggregate quality = %+v, want single bucket with Quality=L3", out)
	}
}

func TestAggregate_OutputSortedAscending(t *testing.T) {
	in := []provider.Reading{
		{Timestamp: mustParseRFC3339(t, "2024-03-01T00:00:00Z"), Value: 1},
		{Timestamp: mustParseRFC3339(t, "2024-01-01T00:00:00Z"), Value: 1},
		{Timestamp: mustParseRFC3339(t, "2024-02-01T00:00:00Z"), Value: 1},
	}
	out := aggregate(in, sampleMonth)
	if len(out) != 3 {
		t.Fatalf("aggregate(month) = %+v, want 3 buckets", out)
	}
	for i := 1; i < len(out); i++ {
		if !out[i-1].Timestamp.Before(out[i].Timestamp) {
			t.Errorf("out[%d].Timestamp %v not before out[%d].Timestamp %v", i-1, out[i-1].Timestamp, i, out[i].Timestamp)
		}
	}
}
