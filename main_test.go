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
	"github.com/welworx/smartmeter-fetch/internal/store/jsonfile"
)

type stubProvider struct {
	points     []provider.Point
	readings   []provider.Reading
	err        error
	fetchCalls int
}

func (s *stubProvider) Name() string { return "evn" }

func (s *stubProvider) ListPoints(ctx context.Context) ([]provider.Point, error) {
	return s.points, s.err
}

func (s *stubProvider) FetchDay(ctx context.Context, pointID string, day time.Time) ([]provider.Reading, error) {
	s.fetchCalls++
	return s.readings, s.err
}

func (s *stubProvider) Location() *time.Location {
	loc, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		panic(err)
	}
	return loc
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
	out := stdout.String()

	// Every flag from both subcommands, with its default, must be present —
	// this is what makes help self-updating instead of hand-copied prose
	// that can drift from the real flags.
	for _, want := range []string{
		"-provider", `default "evn"`,
		"-user", "-password", "-user-agent", "-log-level", "-verbose",
		"-point", "-day", "-from", "-to", "-since-latest", "-data-dir", "-json", "-force",
		"get", "-sample", "-format", "-out",
		"serve", "-addr",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing %q\n---\n%s", want, out)
		}
	}

	// Every env var that influences behavior.
	for _, want := range []string{"SMARTMETER_USER", "SMARTMETER_PASSWORD", "SMARTMETER_DATA_DIR"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing env var %q\n---\n%s", want, out)
		}
	}

	if !strings.Contains(out, "Examples:") {
		t.Errorf("help output missing an Examples section\n---\n%s", out)
	}
}

func TestRun_Help_MatchesNoArgsAndUnknownCommand(t *testing.T) {
	var helpOut, noArgsOut, unknownOut bytes.Buffer
	var discard bytes.Buffer
	run([]string{"help"}, &helpOut, &discard)
	run(nil, &discard, &noArgsOut)
	run([]string{"bogus"}, &discard, &unknownOut)

	if !strings.Contains(noArgsOut.String(), "Examples:") {
		t.Errorf("no-args output missing Examples section (should reuse printUsage): %s", noArgsOut.String())
	}
	if !strings.Contains(unknownOut.String(), "Examples:") {
		t.Errorf("unknown-command output missing Examples section (should reuse printUsage): %s", unknownOut.String())
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

func TestRun_Fetch_NoPointFetchesEveryPointOfDirectLogin(t *testing.T) {
	want := []provider.Reading{{Timestamp: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Value: 1000}}
	withStubProvider(t, &stubProvider{
		points:   []provider.Point{{ID: "AT001"}, {ID: "AT002"}},
		readings: want,
	})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	t.Setenv("SMARTMETER_DATA_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-day", "2024-01-15", "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch, no -point) = %d, stderr = %s", code, stderr.String())
	}

	var got []fetchResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 2 {
		t.Fatalf("results = %+v, want 2 (one per point)", got)
	}
	for i, id := range []string{"AT001", "AT002"} {
		if got[i].Point != id || got[i].Profile != "" || got[i].Unit != provider.Unit || len(got[i].Readings) != 1 {
			t.Errorf("results[%d] = %+v, want point %q with unit %q and 1 reading", i, got[i], id, provider.Unit)
		}
	}
}

func TestRun_Fetch_NoPointNoProfileFetchesEveryProfile(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	withStubProvider(t, &stubProvider{points: []provider.Point{{ID: "AT001"}}})
	var addOut, addErr bytes.Buffer
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	runProfile([]string{"add", "main"}, &addOut, &addErr)
	t.Setenv("SMARTMETER_USER", "bob")
	t.Setenv("SMARTMETER_PASSWORD", "pw2")
	runProfile([]string{"add", "second"}, &addOut, &addErr)
	t.Setenv("SMARTMETER_USER", "")
	t.Setenv("SMARTMETER_PASSWORD", "")
	t.Setenv("SMARTMETER_DATA_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-day", "2024-01-15", "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch, no -point/-profile) = %d, stderr = %s", code, stderr.String())
	}

	var got []fetchResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 2 {
		t.Fatalf("results = %+v, want 2 (one point per profile)", got)
	}
	profiles := map[string]bool{got[0].Profile: true, got[1].Profile: true}
	if !profiles["main"] || !profiles["second"] {
		t.Errorf("results = %+v, want profiles \"main\" and \"second\"", got)
	}
}

func TestRun_Fetch_DayDefaultsToYesterday(t *testing.T) {
	want := []provider.Reading{{Timestamp: time.Now(), Value: 1000}}
	withStubProvider(t, &stubProvider{readings: want})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	t.Setenv("SMARTMETER_DATA_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch, no -day) = %d, stderr = %s", code, stderr.String())
	}
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if !strings.Contains(stderr.String(), "day="+yesterday) {
		t.Errorf("stderr = %q, want day=%s (yesterday)", stderr.String(), yesterday)
	}
}

func TestRun_Fetch_MissingCredentials(t *testing.T) {
	isolateConfigDir(t)
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

func TestRun_Fetch_UsesStoredProfileWhenNoFlags(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	withStubProvider(t, &stubProvider{})
	var addOut, addErr bytes.Buffer
	if code := runProfile([]string{"add", "main"}, &addOut, &addErr); code != 0 {
		t.Fatalf("runProfile(add) = %d, stderr = %s", code, addErr.String())
	}
	t.Setenv("SMARTMETER_USER", "")
	t.Setenv("SMARTMETER_PASSWORD", "")

	want := []provider.Reading{{Timestamp: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Value: 1000}}
	withStubProvider(t, &stubProvider{readings: want})
	t.Setenv("SMARTMETER_DATA_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-day", "2024-01-15", "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch) = %d, stderr = %s", code, stderr.String())
	}
	var got []fetchResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 1 || len(got[0].Readings) != 1 || got[0].Readings[0].Value != 1000 {
		t.Errorf("results = %+v, want 1 result with readings %+v", got, want)
	}
}

func TestRun_Fetch_ProfileFlagSelectsNamedProfile(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	withStubProvider(t, &stubProvider{})
	var addOut, addErr bytes.Buffer
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	runProfile([]string{"add", "main"}, &addOut, &addErr)
	t.Setenv("SMARTMETER_USER", "bob")
	t.Setenv("SMARTMETER_PASSWORD", "pw2")
	runProfile([]string{"add", "second"}, &addOut, &addErr)
	t.Setenv("SMARTMETER_USER", "")
	t.Setenv("SMARTMETER_PASSWORD", "")

	var got []string
	orig := providerFactories["evn"]
	providerFactories["evn"] = func(user, password, userAgent string, logger *slog.Logger) provider.Provider {
		got = []string{user, password}
		return &stubProvider{}
	}
	t.Cleanup(func() { providerFactories["evn"] = orig })

	var stdout, stderr bytes.Buffer
	code := run([]string{"list-points", "-profile", "second"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(list-points -profile second) = %d, stderr = %s", code, stderr.String())
	}
	if len(got) != 2 || got[0] != "bob" || got[1] != "pw2" {
		t.Fatalf("provider credentials = %v, want [bob pw2]", got)
	}
}

func TestRun_Fetch_UnknownProfile(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv("SMARTMETER_PASSPHRASE", "pp")
	t.Setenv("SMARTMETER_USER", "alice")
	t.Setenv("SMARTMETER_PASSWORD", "pw1")
	withStubProvider(t, &stubProvider{})
	var addOut, addErr bytes.Buffer
	runProfile([]string{"add", "main"}, &addOut, &addErr)
	t.Setenv("SMARTMETER_USER", "")
	t.Setenv("SMARTMETER_PASSWORD", "")

	var stdout, stderr bytes.Buffer
	code := run([]string{"list-points", "-profile", "ghost"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("run(list-points, unknown profile) = %d, want 2", code)
	}
	// slog's text handler quotes/escapes attribute values (correct for
	// machine-parseable logs), so match substrings rather than the raw
	// quoted phrase.
	if !strings.Contains(stderr.String(), "no profile") || !strings.Contains(stderr.String(), "ghost") {
		t.Errorf("stderr = %q, want unknown profile message", stderr.String())
	}
}

func TestRun_Fetch_UnknownProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "p1", "-day", "2024-01-15", "-user", "u", "-password", "p", "-provider", "bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("run(fetch, unknown provider) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown provider") || !strings.Contains(stderr.String(), "bogus") {
		t.Errorf("stderr = %q, want unknown provider message", stderr.String())
	}
	if !strings.Contains(stderr.String(), "level=ERROR") {
		t.Errorf("stderr = %q, want a structured log line (level=ERROR), not a bare print", stderr.String())
	}
}

func TestRun_ListPoints(t *testing.T) {
	withStubProvider(t, &stubProvider{points: []provider.Point{{ID: "AT001", Name: "Verbrauch"}}})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"list-points", "-log-level", "debug"}, &stdout, &stderr); code != 0 {
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
		t.Errorf("stderr = %q, want debug log output with -log-level debug", stderr.String())
	}
}

func TestRun_ListPoints_VerboseForcesDebugLevel(t *testing.T) {
	withStubProvider(t, &stubProvider{points: []provider.Point{{ID: "AT001", Name: "Verbrauch"}}})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"list-points", "-verbose"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(list-points, -verbose) = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "listed metering points") {
		t.Errorf("stderr = %q, want debug log output with -verbose", stderr.String())
	}
}

func TestRun_ListPoints_UnknownLogLevel(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"list-points", "-log-level", "bogus", "-user", "u", "-password", "p"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("run(list-points, bad -log-level) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `-log-level "bogus"`) {
		t.Errorf("stderr = %q, want -log-level error message", stderr.String())
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
	want := []provider.Reading{{Timestamp: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Value: 1000}}
	withStubProvider(t, &stubProvider{readings: want})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	dataDir := t.TempDir()
	t.Setenv("SMARTMETER_DATA_DIR", dataDir)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-day", "2024-01-15", "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch) = %d, stderr = %s", code, stderr.String())
	}

	var got []fetchResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 1 || len(got[0].Readings) != 1 || got[0].Readings[0].Value != 1000 || got[0].FetchedAt.IsZero() {
		t.Errorf("results = %+v, want 1 result with readings %+v and non-zero FetchedAt", got, want)
	}
	if strings.Contains(stderr.String(), "fetched readings") {
		t.Errorf("stderr = %q, want no debug output at default -log-level", stderr.String())
	}

	stored, err := jsonfile.New(dataDir).Get(context.Background(), "evn", "AT001", time.Time{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(stored) != 1 || stored[0].Value != 1000 {
		t.Errorf("stored readings = %+v, want the fetched reading persisted", stored)
	}
}

func TestRun_Fetch_NoJSONFlagPrintsNothingToStdout(t *testing.T) {
	want := []provider.Reading{{Timestamp: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Value: 1000}}
	withStubProvider(t, &stubProvider{readings: want})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	t.Setenv("SMARTMETER_DATA_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-day", "2024-01-15"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch) = %d, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty without -json", stdout.String())
	}
}

func TestRun_Fetch_DateRange(t *testing.T) {
	withStubProvider(t, &stubProvider{readings: []provider.Reading{{Timestamp: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Value: 1}}})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	t.Setenv("SMARTMETER_DATA_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-from", "2024-01-15", "-to", "2024-01-17", "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch, -from/-to) = %d, stderr = %s", code, stderr.String())
	}
	var got []fetchResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 3 {
		t.Fatalf("results = %+v, want 3 (one per day, Jan 15-17)", got)
	}
	for i, day := range []string{"2024-01-15", "2024-01-16", "2024-01-17"} {
		if got[i].Day != day {
			t.Errorf("results[%d].Day = %q, want %q", i, got[i].Day, day)
		}
	}
}

func TestRun_Fetch_FromAbsoluteDateDefaultsToToday(t *testing.T) {
	withStubProvider(t, &stubProvider{readings: []provider.Reading{{Timestamp: time.Now(), Value: 1}}})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	t.Setenv("SMARTMETER_DATA_DIR", t.TempDir())

	from := time.Now().AddDate(0, 0, -1)
	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-from", from.Format(dayLayout), "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch, -from without -to) = %d, stderr = %s", code, stderr.String())
	}
	var got []fetchResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	wantDays := []string{from.Format(dayLayout), time.Now().Format(dayLayout)}
	if len(got) != len(wantDays) {
		t.Fatalf("results = %+v, want %d days (-from through today)", got, len(wantDays))
	}
	for i, day := range wantDays {
		if got[i].Day != day {
			t.Errorf("results[%d].Day = %q, want %q", i, got[i].Day, day)
		}
	}
}

func TestRun_Fetch_DayAndFromAreMutuallyExclusive(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-day", "2024-01-15", "-from", "2024-01-15", "-to", "2024-01-16", "-user", "u", "-password", "p"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("run(fetch, -day and -from) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Errorf("stderr = %q, want mutual-exclusivity error", stderr.String())
	}
	if !strings.Contains(stderr.String(), "level=ERROR") {
		t.Errorf("stderr = %q, want a structured log line (level=ERROR), not a bare print", stderr.String())
	}
}

func TestRun_Fetch_OffsetRange(t *testing.T) {
	withStubProvider(t, &stubProvider{readings: []provider.Reading{{Timestamp: time.Now(), Value: 1}}})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	t.Setenv("SMARTMETER_DATA_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-from", "-2", "-to", "-1", "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch, -from/-to as offsets) = %d, stderr = %s", code, stderr.String())
	}
	var got []fetchResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 2 {
		t.Fatalf("results = %+v, want 2 (one per day, offsets -2 and -1)", got)
	}
	wantDays := []string{
		time.Now().AddDate(0, 0, -2).Format(dayLayout),
		time.Now().AddDate(0, 0, -1).Format(dayLayout),
	}
	for i, day := range wantDays {
		if got[i].Day != day {
			t.Errorf("results[%d].Day = %q, want %q", i, got[i].Day, day)
		}
	}
}

func TestRun_Fetch_OffsetFromDefaultsToWithoutTo(t *testing.T) {
	withStubProvider(t, &stubProvider{readings: []provider.Reading{{Timestamp: time.Now(), Value: 1}}})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	t.Setenv("SMARTMETER_DATA_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-from", "-2", "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch, -from offset without -to) = %d, stderr = %s", code, stderr.String())
	}
	var got []fetchResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	wantDays := []string{
		time.Now().AddDate(0, 0, -2).Format(dayLayout),
		time.Now().AddDate(0, 0, -1).Format(dayLayout),
		time.Now().Format(dayLayout),
	}
	if len(got) != len(wantDays) {
		t.Fatalf("results = %+v, want %d days (-from through today)", got, len(wantDays))
	}
	for i, day := range wantDays {
		if got[i].Day != day {
			t.Errorf("results[%d].Day = %q, want %q", i, got[i].Day, day)
		}
	}
}

func TestRun_Fetch_ToWithoutFromErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-to", "-1", "-user", "u", "-password", "p"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("run(fetch, -to without -from) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "-to requires -from") {
		t.Errorf("stderr = %q, want -to-requires-from error", stderr.String())
	}
}

func TestRun_Fetch_ToBeforeFromErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-from", "-1", "-to", "-30", "-user", "u", "-password", "p"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("run(fetch, -to before -from) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "is before") {
		t.Errorf("stderr = %q, want -to-before-from error", stderr.String())
	}
}

func TestRun_Fetch_SinceLatestFallsBackToYesterdayWhenNothingStored(t *testing.T) {
	withStubProvider(t, &stubProvider{readings: []provider.Reading{{Timestamp: time.Now(), Value: 1}}})
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	t.Setenv("SMARTMETER_DATA_DIR", t.TempDir())

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-since-latest", "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch, -since-latest, nothing stored) = %d, stderr = %s", code, stderr.String())
	}
	var got []fetchResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if len(got) != 1 || got[0].Day != yesterday {
		t.Errorf("results = %+v, want 1 result for yesterday (%s)", got, yesterday)
	}
}

// seedYesterday stores one reading for "yesterday" so -since-latest's
// resolved range (latest..yesterday) is exactly that single day — keeping
// these tests independent of how many days have passed since a fixed date.
func seedYesterday(t *testing.T, dataDir string, value float64) string {
	t.Helper()
	day := yesterday()
	if err := jsonfile.New(dataDir).Put(context.Background(), "evn", "AT001", []provider.Reading{
		{Timestamp: day, Value: value},
	}, (&stubProvider{}).Location()); err != nil {
		t.Fatalf("seeding store: %v", err)
	}
	return day.Format(dayLayout)
}

func TestRun_Fetch_SinceLatestSkipsAlreadyStoredBoundaryDayByDefault(t *testing.T) {
	dataDir := t.TempDir()
	dayStr := seedYesterday(t, dataDir, 1)
	stub := &stubProvider{readings: []provider.Reading{{Timestamp: yesterday(), Value: 2}}}
	withStubProvider(t, stub)
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	t.Setenv("SMARTMETER_DATA_DIR", dataDir)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-since-latest", "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch, -since-latest) = %d, stderr = %s", code, stderr.String())
	}
	var got []fetchResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 1 || got[0].Day != dayStr || !got[0].Skipped {
		t.Fatalf("results = %+v, want a single skipped result for %s (already stored)", got, dayStr)
	}
	if stub.fetchCalls != 0 {
		t.Errorf("fetchCalls = %d, want 0: the already-stored boundary day should not hit the portal without -force", stub.fetchCalls)
	}
	stored, err := jsonfile.New(dataDir).Get(context.Background(), "evn", "AT001", time.Time{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(stored) != 1 || stored[0].Value != 1 {
		t.Errorf("stored = %+v, want the original seeded reading (value 1) unchanged", stored)
	}
}

func TestRun_Fetch_SinceLatestForceRefetchesStoredBoundaryDay(t *testing.T) {
	dataDir := t.TempDir()
	dayStr := seedYesterday(t, dataDir, 1)
	stub := &stubProvider{readings: []provider.Reading{{Timestamp: yesterday(), Value: 2}}}
	withStubProvider(t, stub)
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	t.Setenv("SMARTMETER_DATA_DIR", dataDir)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-since-latest", "-force", "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch, -since-latest -force) = %d, stderr = %s", code, stderr.String())
	}
	var got []fetchResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 1 || got[0].Day != dayStr || got[0].Skipped {
		t.Fatalf("results = %+v, want a single re-fetched (not skipped) result for %s", got, dayStr)
	}
	if stub.fetchCalls != 1 {
		t.Errorf("fetchCalls = %d, want 1: -force should hit the portal even for an already-stored day", stub.fetchCalls)
	}
	stored, err := jsonfile.New(dataDir).Get(context.Background(), "evn", "AT001", time.Time{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(stored) != 1 || stored[0].Value != 2 {
		t.Errorf("stored = %+v, want the revised reading (value 2) after -force", stored)
	}
}

func TestRun_Fetch_SkipsAlreadyStoredDayByDefault(t *testing.T) {
	dataDir := t.TempDir()
	if err := jsonfile.New(dataDir).Put(context.Background(), "evn", "AT001", []provider.Reading{
		{Timestamp: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Value: 1},
	}, (&stubProvider{}).Location()); err != nil {
		t.Fatalf("seeding store: %v", err)
	}
	stub := &stubProvider{readings: []provider.Reading{{Timestamp: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Value: 2}}}
	withStubProvider(t, stub)
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	t.Setenv("SMARTMETER_DATA_DIR", dataDir)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-day", "2024-01-15", "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch, -day already stored) = %d, stderr = %s", code, stderr.String())
	}
	var got []fetchResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 1 || !got[0].Skipped || len(got[0].Readings) != 0 {
		t.Fatalf("results = %+v, want a single skipped result with no readings", got)
	}
	if stub.fetchCalls != 0 {
		t.Errorf("fetchCalls = %d, want 0: an already-stored day should not hit the portal without -force", stub.fetchCalls)
	}
}

func TestRun_Fetch_ForceRefetchesStoredDay(t *testing.T) {
	dataDir := t.TempDir()
	if err := jsonfile.New(dataDir).Put(context.Background(), "evn", "AT001", []provider.Reading{
		{Timestamp: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Value: 1},
	}, (&stubProvider{}).Location()); err != nil {
		t.Fatalf("seeding store: %v", err)
	}
	stub := &stubProvider{readings: []provider.Reading{{Timestamp: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Value: 2}}}
	withStubProvider(t, stub)
	t.Setenv("SMARTMETER_USER", "u")
	t.Setenv("SMARTMETER_PASSWORD", "p")
	t.Setenv("SMARTMETER_DATA_DIR", dataDir)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch", "-point", "AT001", "-day", "2024-01-15", "-force", "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(fetch, -day -force) = %d, stderr = %s", code, stderr.String())
	}
	var got []fetchResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding stdout: %v (stdout = %s)", err, stdout.String())
	}
	if len(got) != 1 || got[0].Skipped || len(got[0].Readings) != 1 || got[0].Readings[0].Value != 2 {
		t.Fatalf("results = %+v, want a single re-fetched result with the revised reading", got)
	}
	if stub.fetchCalls != 1 {
		t.Errorf("fetchCalls = %d, want 1", stub.fetchCalls)
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
