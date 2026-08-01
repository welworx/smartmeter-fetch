// get_test.go
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
)

func TestRun_Get_TextOutputDefault(t *testing.T) {
	withStubProvider(t, &stubProvider{readings: []provider.Reading{
		{Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), Value: 42},
	}})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	dataDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := run([]string{"get", "-point", "AT001", "-day", "2024-01-15", "-data-dir", dataDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(get) = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "42") {
		t.Errorf("stdout = %q, want it to contain the fetched value", stdout.String())
	}
}

func TestRun_Get_FetchesWhenNotStoredThenReadsBack(t *testing.T) {
	stub := &stubProvider{readings: []provider.Reading{
		{Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), Value: 42},
	}}
	withStubProvider(t, stub)
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	dataDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"get", "-point", "AT001", "-day", "2024-01-15", "-data-dir", dataDir, "-format", "json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(get) first call = %d, stderr = %s", code, stderr.String())
	}
	if stub.fetchCalls != 1 {
		t.Fatalf("fetchCalls after first get = %d, want 1", stub.fetchCalls)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"get", "-point", "AT001", "-day", "2024-01-15", "-data-dir", dataDir, "-format", "json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(get) second call = %d, stderr = %s", code, stderr.String())
	}
	if stub.fetchCalls != 1 {
		t.Errorf("fetchCalls after second get = %d, want still 1 (day already stored, should not re-fetch)", stub.fetchCalls)
	}
	var got []outputRow
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 1 || got[0].Value != 42 {
		t.Errorf("second get's output = %+v, want the stored reading", got)
	}
}

func TestRun_Get_SampleAggregatesOutput(t *testing.T) {
	withStubProvider(t, &stubProvider{readings: []provider.Reading{
		{Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), Value: 100},
		{Timestamp: time.Date(2024, 1, 15, 10, 15, 0, 0, time.UTC), Value: 200},
	}})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	dataDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := run([]string{"get", "-point", "AT001", "-day", "2024-01-15", "-data-dir", dataDir, "-format", "json", "-sample", "hour"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(get -sample hour) = %d, stderr = %s", code, stderr.String())
	}
	var got []outputRow
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 1 || got[0].Value != 300 {
		t.Errorf("aggregated output = %+v, want single 300-value bucket", got)
	}
}

// TestRun_Get_ViennaDayBoundary covers a full Vienna-local day, which in
// winter (CET, UTC+1) starts at 23:00 UTC the day before — the case where a
// UTC-bounded read-back window silently drops the first hour of readings, and
// where UTC-formatted -out date placeholders name the wrong day.
func TestRun_Get_ViennaDayBoundary(t *testing.T) {
	// 96 quarter-hours from Vienna midnight of 2024-01-15 (= 2024-01-14T23:00Z)
	// through 2024-01-15T22:45Z, 1 Wh each.
	start := time.Date(2024, 1, 14, 23, 0, 0, 0, time.UTC)
	readings := make([]provider.Reading, 96)
	for i := range readings {
		readings[i] = provider.Reading{Timestamp: start.Add(time.Duration(i) * 15 * time.Minute), Value: 1}
	}
	withStubProvider(t, &stubProvider{readings: readings})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	dataDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := run([]string{"get", "-point", "AT001", "-day", "2024-01-15", "-data-dir", dataDir, "-format", "json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(get) = %d, stderr = %s", code, stderr.String())
	}
	var got []outputRow
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 96 {
		t.Fatalf("len(readings) = %d, want 96 (the full Vienna day, nothing dropped or added)", len(got))
	}
	if !got[0].Timestamp.Equal(start) {
		t.Errorf("first timestamp = %s, want %s", got[0].Timestamp.Format(time.RFC3339), start.Format(time.RFC3339))
	}
	wantLast := time.Date(2024, 1, 15, 22, 45, 0, 0, time.UTC)
	if !got[95].Timestamp.Equal(wantLast) {
		t.Errorf("last timestamp = %s, want %s", got[95].Timestamp.Format(time.RFC3339), wantLast.Format(time.RFC3339))
	}

	stdout.Reset()
	code = run([]string{"get", "-point", "AT001", "-day", "2024-01-15", "-data-dir", dataDir, "-format", "json", "-sample", "day"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(get -sample day) = %d, stderr = %s", code, stderr.String())
	}
	got = nil
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 1 {
		t.Fatalf("len(day buckets) = %d, want 1 (one Vienna day)", len(got))
	}
	if got[0].Value != 96 {
		t.Errorf("day bucket value = %v, want 96 (sum of all 96 readings)", got[0].Value)
	}
	if !got[0].Timestamp.Equal(start) {
		t.Errorf("day bucket start = %s, want %s (Vienna midnight)", got[0].Timestamp.Format(time.RFC3339), start.Format(time.RFC3339))
	}

	// The bucket's UTC timestamp is 2024-01-14T23:00Z, so UTC-formatted date
	// placeholders would name the file for the 14th instead of the 15th.
	outDir := t.TempDir()
	stdout.Reset()
	code = run([]string{"get", "-point", "AT001", "-day", "2024-01-15", "-data-dir", dataDir, "-sample", "day",
		"-out", filepath.Join(outDir, "<yyyy>-<mm>-<dd>.csv")}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(get -out) = %d, stderr = %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(outDir, "2024-01-15.csv")); err != nil {
		entries, _ := os.ReadDir(outDir)
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("want -out file 2024-01-15.csv (Vienna day), got %v", names)
	}
}

func TestRun_Get_CSVToStdoutWithMultiplePointsErrors(t *testing.T) {
	withStubProvider(t, &stubProvider{
		points:   []provider.Point{{ID: "AT001", Name: "Consumption"}, {ID: "AT002", Name: "Production"}},
		readings: []provider.Reading{{Timestamp: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Value: 1}},
	})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	dataDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := run([]string{"get", "-day", "2024-01-15", "-data-dir", dataDir, "-format", "csv"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("run(get -format csv, multiple points) = 0, want a non-zero exit code")
	}
	if !strings.Contains(stderr.String(), "csv") {
		t.Errorf("stderr = %q, want it to mention csv/single point", stderr.String())
	}
}

func TestRun_Get_OutWritesTemplatedFile(t *testing.T) {
	withStubProvider(t, &stubProvider{readings: []provider.Reading{
		{Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), Value: 42},
	}})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	dataDir := t.TempDir()
	outDir := t.TempDir()
	tmpl := filepath.Join(outDir, "<zaehlerpunkt_id>", "<yyyy>", "data.csv")

	var stdout, stderr bytes.Buffer
	code := run([]string{"get", "-point", "AT001", "-day", "2024-01-15", "-data-dir", dataDir, "-out", tmpl}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(get -out) = %d, stderr = %s", code, stderr.String())
	}
	want := filepath.Join(outDir, "1", "2024", "data.csv")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v (outDir contents not as expected)", want, err)
	}
	if !strings.Contains(string(data), "42") {
		t.Errorf("%s = %s, want it to contain the fetched value", want, data)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty when -out is set", stdout.String())
	}
}

func TestRun_Get_OutWithoutPointPlaceholderAndMultiplePointsErrors(t *testing.T) {
	withStubProvider(t, &stubProvider{
		points:   []provider.Point{{ID: "AT001"}, {ID: "AT002"}},
		readings: []provider.Reading{{Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), Value: 1}},
	})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	dataDir := t.TempDir()
	outDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := run([]string{"get", "-day", "2024-01-15", "-data-dir", dataDir, "-out", filepath.Join(outDir, "<yyyy>.csv")}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run(get -out without point placeholder, multiple points) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "zaehlerpunkt") {
		t.Errorf("stderr = %q, want it to suggest a point placeholder", stderr.String())
	}
}

func TestRun_Get_ForcePassesThroughToRefetch(t *testing.T) {
	stub := &stubProvider{readings: []provider.Reading{
		{Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), Value: 1},
	}}
	withStubProvider(t, stub)
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	dataDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	run([]string{"get", "-point", "AT001", "-day", "2024-01-15", "-data-dir", dataDir}, &stdout, &stderr)
	if stub.fetchCalls != 1 {
		t.Fatalf("fetchCalls after first get = %d, want 1", stub.fetchCalls)
	}
	stdout.Reset()
	run([]string{"get", "-point", "AT001", "-day", "2024-01-15", "-data-dir", dataDir, "-force"}, &stdout, &stderr)
	if stub.fetchCalls != 2 {
		t.Errorf("fetchCalls after -force get = %d, want 2", stub.fetchCalls)
	}
}

func TestRun_Get_FetchFailureDoesNotReturnStaleData(t *testing.T) {
	withStubProvider(t, &stubProvider{err: errStub})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	dataDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := run([]string{"get", "-point", "AT001", "-day", "2024-01-15", "-data-dir", dataDir}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("run(get, fetch error) = 0, want non-zero")
	}
	if !strings.Contains(stderr.String(), "level=ERROR") {
		t.Errorf("stderr = %q, want a structured error log line", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty (no stale/empty data on fetch failure)", stdout.String())
	}
}

func TestRun_Get_UnrecognizedSampleErrors(t *testing.T) {
	withStubProvider(t, &stubProvider{})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")

	var stdout, stderr bytes.Buffer
	code := run([]string{"get", "-point", "AT001", "-day", "2024-01-15", "-sample", "fortnight"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("run(get -sample fortnight) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "level=ERROR") {
		t.Errorf("stderr = %q, want a structured error log line", stderr.String())
	}
}
