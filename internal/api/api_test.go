package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
	"github.com/welworx/smartmeter-fetch/internal/store"
	"github.com/welworx/smartmeter-fetch/internal/store/jsonfile"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("time.Parse(%q): %v", s, err)
	}
	return ts
}

func TestPoints_ListsStoredPoints(t *testing.T) {
	st := jsonfile.New(t.TempDir())
	ctx := context.Background()
	if err := st.Put(ctx, "evn", "AT001", []provider.Reading{{Timestamp: mustParse(t, "2024-01-15T00:00:00Z"), Value: 1}}, time.UTC); err != nil {
		t.Fatalf("Put: %v", err)
	}

	srv := httptest.NewServer(NewHandler(st, testLogger()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/points")
	if err != nil {
		t.Fatalf("GET /v1/points: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got []store.PointRef
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	want := store.PointRef{Provider: "evn", ID: "AT001"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("points = %+v, want [%+v]", got, want)
	}
}

func TestPoints_EmptyStoreReturnsEmptyArrayNotNull(t *testing.T) {
	st := jsonfile.New(t.TempDir())
	srv := httptest.NewServer(NewHandler(st, testLogger()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/points")
	if err != nil {
		t.Fatalf("GET /v1/points: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(body) != "[]\n" {
		t.Errorf("body = %q, want %q (empty array, not null)", body, "[]\n")
	}
}

func TestReadings_ReturnsStoredReadings(t *testing.T) {
	st := jsonfile.New(t.TempDir())
	ctx := context.Background()
	want := []provider.Reading{{Timestamp: mustParse(t, "2024-01-15T00:00:00Z"), Value: 42}}
	if err := st.Put(ctx, "evn", "AT001", want, time.UTC); err != nil {
		t.Fatalf("Put: %v", err)
	}

	srv := httptest.NewServer(NewHandler(st, testLogger()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/readings?point=AT001")
	if err != nil {
		t.Fatalf("GET /v1/readings: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got []provider.Reading
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if len(got) != 1 || got[0].Value != 42 {
		t.Errorf("readings = %+v, want one reading with value 42", got)
	}
}

func TestReadings_SinceFiltersOlderReadings(t *testing.T) {
	st := jsonfile.New(t.TempDir())
	ctx := context.Background()
	readings := []provider.Reading{
		{Timestamp: mustParse(t, "2024-01-15T00:00:00Z"), Value: 1},
		{Timestamp: mustParse(t, "2024-01-16T00:00:00Z"), Value: 2},
	}
	if err := st.Put(ctx, "evn", "AT001", readings, time.UTC); err != nil {
		t.Fatalf("Put: %v", err)
	}

	srv := httptest.NewServer(NewHandler(st, testLogger()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/readings?point=AT001&since=2024-01-16T00:00:00Z")
	if err != nil {
		t.Fatalf("GET /v1/readings: %v", err)
	}
	defer resp.Body.Close()

	var got []provider.Reading
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if len(got) != 1 || got[0].Value != 2 {
		t.Errorf("readings(since) = %+v, want only the Jan16 reading", got)
	}
}

func TestReadings_MissingPointParamIs400(t *testing.T) {
	st := jsonfile.New(t.TempDir())
	srv := httptest.NewServer(NewHandler(st, testLogger()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/readings")
	if err != nil {
		t.Fatalf("GET /v1/readings: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestReadings_UnknownPointIs404(t *testing.T) {
	st := jsonfile.New(t.TempDir())
	srv := httptest.NewServer(NewHandler(st, testLogger()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/readings?point=ghost")
	if err != nil {
		t.Fatalf("GET /v1/readings: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestReadings_MalformedSinceIs400(t *testing.T) {
	st := jsonfile.New(t.TempDir())
	ctx := context.Background()
	if err := st.Put(ctx, "evn", "AT001", []provider.Reading{{Timestamp: mustParse(t, "2024-01-15T00:00:00Z"), Value: 1}}, time.UTC); err != nil {
		t.Fatalf("Put: %v", err)
	}
	srv := httptest.NewServer(NewHandler(st, testLogger()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/readings?point=AT001&since=not-a-date")
	if err != nil {
		t.Fatalf("GET /v1/readings: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestOpenAPI_ServesSpec(t *testing.T) {
	st := jsonfile.New(t.TempDir())
	srv := httptest.NewServer(NewHandler(st, testLogger()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/openapi.json")
	if err != nil {
		t.Fatalf("GET /openapi.json: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var spec map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&spec); err != nil {
		t.Fatalf("decoding body as JSON: %v", err)
	}
	if spec["openapi"] == nil {
		t.Errorf("spec = %+v, want an \"openapi\" version field", spec)
	}
}
