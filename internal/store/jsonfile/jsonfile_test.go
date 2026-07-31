package jsonfile

import (
	"context"
	"testing"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
)

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("time.Parse(%q): %v", s, err)
	}
	return ts
}

func TestPutGet_RoundTrip(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	readings := []provider.Reading{
		{Timestamp: mustParse(t, "2024-01-15T00:15:00Z"), Value: 100},
		{Timestamp: mustParse(t, "2024-01-15T00:00:00Z"), Value: 50},
	}
	if err := s.Put(ctx, "evn", "AT001", readings); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(ctx, "evn", "AT001", time.Time{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Get = %+v, want 2 readings", got)
	}
	if got[0].Value != 50 || got[1].Value != 100 {
		t.Errorf("Get = %+v, want ascending by timestamp (50, 100)", got)
	}
}

func TestPut_ReplacesDayInsteadOfDuplicating(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	day := []provider.Reading{{Timestamp: mustParse(t, "2024-01-15T00:00:00Z"), Value: 1, Quality: "L3"}}
	if err := s.Put(ctx, "evn", "AT001", day); err != nil {
		t.Fatalf("Put (first): %v", err)
	}
	revised := []provider.Reading{{Timestamp: mustParse(t, "2024-01-15T00:00:00Z"), Value: 2, Quality: "L2"}}
	if err := s.Put(ctx, "evn", "AT001", revised); err != nil {
		t.Fatalf("Put (revised): %v", err)
	}

	got, err := s.Get(ctx, "evn", "AT001", time.Time{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 1 || got[0].Value != 2 || got[0].Quality != "L2" {
		t.Errorf("Get after revised Put = %+v, want single revised reading", got)
	}
}

func TestPut_SplitsReadingsAcrossDayFiles(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	readings := []provider.Reading{
		{Timestamp: mustParse(t, "2024-01-15T23:45:00Z"), Value: 1},
		{Timestamp: mustParse(t, "2024-01-16T00:00:00Z"), Value: 2},
	}
	if err := s.Put(ctx, "evn", "AT001", readings); err != nil {
		t.Fatalf("Put: %v", err)
	}

	latest, found, err := s.Latest(ctx, "evn", "AT001")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !found {
		t.Fatal("Latest: found = false, want true")
	}
	if want := mustParse(t, "2024-01-16T00:00:00Z"); !latest.Equal(want) {
		t.Errorf("Latest = %v, want %v", latest, want)
	}
}

func TestGet_SinceFiltersOlderReadings(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	readings := []provider.Reading{
		{Timestamp: mustParse(t, "2024-01-15T00:00:00Z"), Value: 1},
		{Timestamp: mustParse(t, "2024-01-16T00:00:00Z"), Value: 2},
	}
	if err := s.Put(ctx, "evn", "AT001", readings); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(ctx, "evn", "AT001", mustParse(t, "2024-01-16T00:00:00Z"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 1 || got[0].Value != 2 {
		t.Errorf("Get(since=Jan16) = %+v, want only the Jan16 reading", got)
	}
}

func TestGet_UnknownPointReturnsEmpty(t *testing.T) {
	s := New(t.TempDir())
	got, err := s.Get(context.Background(), "evn", "ghost", time.Time{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Get(unknown point) = %+v, want empty", got)
	}
}

func TestLatest_UnknownPointReturnsNotFound(t *testing.T) {
	s := New(t.TempDir())
	_, found, err := s.Latest(context.Background(), "evn", "ghost")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if found {
		t.Error("Latest(unknown point): found = true, want false")
	}
}

func TestPut_EmptyReadingsIsNoop(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Put(context.Background(), "evn", "AT001", nil); err != nil {
		t.Fatalf("Put(nil): %v", err)
	}
	_, found, err := s.Latest(context.Background(), "evn", "AT001")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if found {
		t.Error("Latest after Put(nil): found = true, want false")
	}
}
