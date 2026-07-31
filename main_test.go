package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
)

type stubProvider struct {
	points   []provider.Point
	readings []provider.Reading
	err      error
}

func (s *stubProvider) Name() string { return "evn" }

func (s *stubProvider) ListPoints(ctx context.Context) ([]provider.Point, error) {
	return s.points, s.err
}

func (s *stubProvider) FetchDay(ctx context.Context, pointID string, day time.Time) ([]provider.Reading, error) {
	return s.readings, s.err
}

func withStubProvider(t *testing.T, stub *stubProvider) {
	t.Helper()
	orig := providerFactories["evn"]
	providerFactories["evn"] = func(user, password, userAgent string, logger *slog.Logger) provider.Provider { return stub }
	t.Cleanup(func() { providerFactories["evn"] = orig })
}

func TestRun_NoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Errorf("run(nil) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("stderr = %q, want usage message", stderr.String())
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"bogus"}, &stdout, &stderr); code != 2 {
		t.Errorf("run(bogus) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "bogus"`) {
		t.Errorf("stderr = %q, want unknown command message", stderr.String())
	}
}

func TestRun_Help(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, &stdout, &stderr); code != 0 {
		t.Errorf("run(help) = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Errorf("stdout = %q, want usage message", stdout.String())
	}
}

func TestRun_Version(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Errorf("run(version) = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "smartmeter-fetch") {
		t.Errorf("stdout = %q, want version string", stdout.String())
	}
}

func TestRun_Fetch_MissingPoint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-day", "2024-01-15", "-user", "u", "-password", "p"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("run(fetch, no -point) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "-point is required") {
		t.Errorf("stderr = %q, want missing -point message", stderr.String())
	}
}

func TestRun_Fetch_MissingCredentials(t *testing.T) {
	t.Setenv("SMARTMETER_USER", "")
	t.Setenv("SMARTMETER_PASSWORD", "")
	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "p1", "-day", "2024-01-15"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("run(fetch, no creds) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "missing credentials") {
		t.Errorf("stderr = %q, want missing credentials message", stderr.String())
	}
}

func TestRun_Fetch_UnknownProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "p1", "-day", "2024-01-15", "-user", "u", "-password", "p", "-provider", "bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("run(fetch, unknown provider) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown provider "bogus"`) {
		t.Errorf("stderr = %q, want unknown provider message", stderr.String())
	}
}

func TestRun_ListPoints(t *testing.T) {
	withStubProvider(t, &stubProvider{points: []provider.Point{{ID: "AT001", Name: "Verbrauch"}}})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"list-points", "-v"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(list-points) = %d, stderr = %s", code, stderr.String())
	}

	var points []provider.Point
	if err := json.Unmarshal(stdout.Bytes(), &points); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(points) != 1 || points[0].ID != "AT001" {
		t.Errorf("points = %+v, want one point with ID AT001", points)
	}
	if !strings.Contains(stderr.String(), "listing metering points") {
		t.Errorf("stderr = %q, want log output", stderr.String())
	}
	if !strings.Contains(stderr.String(), "listed metering points") {
		t.Errorf("stderr = %q, want debug log output with -v", stderr.String())
	}
}

func TestRun_ListPoints_ProviderError(t *testing.T) {
	withStubProvider(t, &stubProvider{err: errStub})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")

	var stdout, stderr bytes.Buffer
	code := run([]string{"list-points"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("run(list-points, provider error) = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "listing metering points failed") {
		t.Errorf("stderr = %q, want error log", stderr.String())
	}
}

func TestRun_Fetch(t *testing.T) {
	want := []provider.Reading{{Timestamp: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), ValueWh: 1000}}
	withStubProvider(t, &stubProvider{readings: want})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-day", "2024-01-15"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch) = %d, stderr = %s", code, stderr.String())
	}

	var got []provider.Reading
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 1 || got[0].ValueWh != 1000 {
		t.Errorf("readings = %+v, want %+v", got, want)
	}
	if strings.Contains(stderr.String(), "fetched readings") {
		t.Errorf("stderr = %q, want no debug output without -v", stderr.String())
	}
}

func TestRun_Fetch_BadDay(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-day", "not-a-date", "-user", "u", "-password", "p"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("run(fetch, bad day) = %d, want 2", code)
	}
}

type stubError string

func (e stubError) Error() string { return string(e) }

const errStub = stubError("stub provider error")
