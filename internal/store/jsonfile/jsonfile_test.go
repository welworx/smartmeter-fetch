package jsonfile

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
	"github.com/welworx/smartmeter-fetch/internal/store"
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
	if err := s.Put(ctx, "evn", "AT001", readings, time.UTC); err != nil {
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
	if err := s.Put(ctx, "evn", "AT001", day, time.UTC); err != nil {
		t.Fatalf("Put (first): %v", err)
	}
	revised := []provider.Reading{{Timestamp: mustParse(t, "2024-01-15T00:00:00Z"), Value: 2, Quality: "L2"}}
	if err := s.Put(ctx, "evn", "AT001", revised, time.UTC); err != nil {
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
	if err := s.Put(ctx, "evn", "AT001", readings, time.UTC); err != nil {
		t.Fatalf("Put: %v", err)
	}

	latest, found, err := s.Latest(ctx, "evn", "AT001", time.UTC)
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
	if err := s.Put(ctx, "evn", "AT001", readings, time.UTC); err != nil {
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
	_, found, err := s.Latest(context.Background(), "evn", "ghost", time.UTC)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if found {
		t.Error("Latest(unknown point): found = true, want false")
	}
}

func TestHas(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()

	has, err := s.Has(ctx, "evn", "AT001", mustParse(t, "2024-01-15T00:00:00Z"), time.UTC)
	if err != nil {
		t.Fatalf("Has (before Put): %v", err)
	}
	if has {
		t.Error("Has (before Put) = true, want false")
	}

	if err := s.Put(ctx, "evn", "AT001", []provider.Reading{
		{Timestamp: mustParse(t, "2024-01-15T00:00:00Z"), Value: 1},
	}, time.UTC); err != nil {
		t.Fatalf("Put: %v", err)
	}

	has, err = s.Has(ctx, "evn", "AT001", mustParse(t, "2024-01-15T12:00:00Z"), time.UTC)
	if err != nil {
		t.Fatalf("Has (after Put, same day): %v", err)
	}
	if !has {
		t.Error("Has (after Put, same day) = false, want true")
	}

	has, err = s.Has(ctx, "evn", "AT001", mustParse(t, "2024-01-16T00:00:00Z"), time.UTC)
	if err != nil {
		t.Fatalf("Has (different day): %v", err)
	}
	if has {
		t.Error("Has (different day) = true, want false")
	}
}

func TestPut_EmptyReadingsIsNoop(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Put(context.Background(), "evn", "AT001", nil, time.UTC); err != nil {
		t.Fatalf("Put(nil): %v", err)
	}
	_, found, err := s.Latest(context.Background(), "evn", "AT001", time.UTC)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if found {
		t.Error("Latest after Put(nil): found = true, want false")
	}
}

// buildViennaDayReadings returns a full Vienna-local day's worth of
// quarter-hourly readings for day (Y/M/D used as-is, matching how
// evn.FetchDay constructs them from a requested day) — 96 readings on a
// normal day, fewer/more on a DST-transition day, mirroring the real
// provider's output shape.
func buildViennaDayReadings(t *testing.T, loc *time.Location, year int, month time.Month, day int, count int, value float64) []provider.Reading {
	t.Helper()
	midnight := time.Date(year, month, day, 0, 0, 0, 0, loc)
	readings := make([]provider.Reading, count)
	for i := range readings {
		readings[i] = provider.Reading{
			Timestamp: midnight.Add(time.Duration(i) * 15 * time.Minute).UTC(),
			Value:     value,
		}
	}
	return readings
}

func TestPut_AscendingDaysDoNotClobberEachOther(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	loc := testViennaLocation(t)

	day14 := buildViennaDayReadings(t, loc, 2024, 1, 14, 96, 1)
	day15 := buildViennaDayReadings(t, loc, 2024, 1, 15, 96, 2)

	if err := s.Put(ctx, "evn", "AT001", day14, loc); err != nil {
		t.Fatalf("Put(day14): %v", err)
	}
	if err := s.Put(ctx, "evn", "AT001", day15, loc); err != nil {
		t.Fatalf("Put(day15): %v", err)
	}

	got, err := s.Get(ctx, "evn", "AT001", time.Time{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 192 {
		t.Fatalf("Get after two ascending Puts = %d readings, want 192 (96+96, neither day clobbered)", len(got))
	}

	day14Count, day15Count := 0, 0
	for _, r := range got {
		switch r.Value {
		case 1:
			day14Count++
		case 2:
			day15Count++
		}
	}
	if day14Count != 96 {
		t.Errorf("day14Count = %d, want 96 (day14's file must not have been overwritten by day15's spillover)", day14Count)
	}
	if day15Count != 96 {
		t.Errorf("day15Count = %d, want 96", day15Count)
	}

	dir, err := s.pointDir("evn", "AT001")
	if err != nil {
		t.Fatalf("pointDir: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("day files = %v, want exactly 2 (one per Vienna day, no UTC-day splitting)", names)
	}
}

func TestPut_DSTSpringForward(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	loc := testViennaLocation(t)

	// 2024-03-31 is Vienna's spring-forward day: 02:00-03:00 doesn't
	// exist, so the day is 23 hours long — 92 quarter-hour readings.
	readings := buildViennaDayReadings(t, loc, 2024, 3, 31, 92, 5)
	if err := s.Put(ctx, "evn", "AT001", readings, loc); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(ctx, "evn", "AT001", time.Time{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 92 {
		t.Fatalf("Get = %d readings, want 92 (23-hour spring-forward day, none dropped)", len(got))
	}
}

func TestPut_DSTFallBack(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	loc := testViennaLocation(t)

	// 2024-10-27 is Vienna's fall-back day: 02:00-03:00 happens twice,
	// so the day is 25 hours long — 100 quarter-hour readings.
	readings := buildViennaDayReadings(t, loc, 2024, 10, 27, 100, 7)
	if err := s.Put(ctx, "evn", "AT001", readings, loc); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(ctx, "evn", "AT001", time.Time{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 100 {
		t.Fatalf("Get = %d readings, want 100 (25-hour fall-back day, none dropped or duplicated)", len(got))
	}
}

func testViennaLocation(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatalf("time.LoadLocation(Europe/Vienna): %v", err)
	}
	return loc
}

func TestListPoints_EmptyStore(t *testing.T) {
	s := New(t.TempDir())
	got, err := s.ListPoints(context.Background())
	if err != nil {
		t.Fatalf("ListPoints: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListPoints(empty store) = %+v, want empty", got)
	}
}

func TestListPoints_MissingDir(t *testing.T) {
	s := New(t.TempDir() + "/does-not-exist")
	got, err := s.ListPoints(context.Background())
	if err != nil {
		t.Fatalf("ListPoints: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListPoints(missing dir) = %+v, want empty", got)
	}
}

func TestListPoints_MultipleProvidersAndPointsSortedByProviderThenID(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	one := []provider.Reading{{Timestamp: mustParse(t, "2024-01-15T00:00:00Z"), Value: 1}}
	if err := s.Put(ctx, "evn", "AT002", one, time.UTC); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put(ctx, "evn", "AT001", one, time.UTC); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put(ctx, "otherprovider", "XY001", one, time.UTC); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.ListPoints(ctx)
	if err != nil {
		t.Fatalf("ListPoints: %v", err)
	}
	want := []store.PointRef{
		{Provider: "evn", ID: "AT001"},
		{Provider: "evn", ID: "AT002"},
		{Provider: "otherprovider", ID: "XY001"},
	}
	if len(got) != len(want) {
		t.Fatalf("ListPoints = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ListPoints[%d] = %+v, want %+v (want sorted by provider then id)", i, got[i], want[i])
		}
	}
}

// TestRejectsPathTraversalInProviderOrPointID guards against CWE-22: since
// internal/api resolves an HTTP query parameter into a pointID before
// calling into this store, providerName/pointID must never be trusted to
// stay within Dir on their own.
func TestRejectsPathTraversalInProviderOrPointID(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	reading := []provider.Reading{{Timestamp: mustParse(t, "2024-01-15T00:00:00Z"), Value: 1}}

	badNames := []string{"..", "../etc", "a/../../etc", "a/b", `a\b`, ""}
	for _, bad := range badNames {
		if err := s.Put(ctx, bad, "AT001", reading, time.UTC); err == nil {
			t.Errorf("Put(providerName=%q): want error, got nil", bad)
		}
		if err := s.Put(ctx, "evn", bad, reading, time.UTC); err == nil {
			t.Errorf("Put(pointID=%q): want error, got nil", bad)
		}
		if _, err := s.Get(ctx, bad, "AT001", time.Time{}); err == nil {
			t.Errorf("Get(providerName=%q): want error, got nil", bad)
		}
		if _, err := s.Get(ctx, "evn", bad, time.Time{}); err == nil {
			t.Errorf("Get(pointID=%q): want error, got nil", bad)
		}
		if _, _, err := s.Latest(ctx, "evn", bad, time.UTC); err == nil {
			t.Errorf("Latest(pointID=%q): want error, got nil", bad)
		}
		if _, err := s.Has(ctx, "evn", bad, mustParse(t, "2024-01-15T00:00:00Z"), time.UTC); err == nil {
			t.Errorf("Has(pointID=%q): want error, got nil", bad)
		}
	}
}

// TestGetRefusesToEscapeDirViaSymlink covers what name validation alone
// cannot: a point directory that is a symlink pointing outside Dir. Get
// resolves its reads under an os.Root pinned to Dir, so the OS itself
// refuses to follow the link out.
func TestGetRefusesToEscapeDirViaSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "2024-01-15.json"), []byte(`[]`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "evn"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "evn", "AT001")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if _, err := New(dir).Get(context.Background(), "evn", "AT001", time.Time{}); err == nil {
		t.Error("Get through an escaping symlink: want error, got nil")
	}
}
