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
