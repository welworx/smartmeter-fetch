package evn

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
)

func TestProvider_Name(t *testing.T) {
	p := New("user", "pass")
	if got := p.Name(); got != "evn" {
		t.Errorf("Name() = %q, want %q", got, "evn")
	}
}

func TestProvider_Location(t *testing.T) {
	p := New("user", "pass")
	loc := p.Location()
	if loc == nil {
		t.Fatal("Location() = nil")
	}
	// Vienna is UTC+1 in January (no DST) — a location-sensitive way to
	// confirm this is really Europe/Vienna and not e.g. UTC or a
	// mislabeled fixed offset.
	got := time.Date(2024, time.January, 15, 12, 0, 0, 0, loc)
	want := time.Date(2024, time.January, 15, 11, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Location() offset wrong: 2024-01-15T12:00 in Location() = %v, want %v (UTC+1)", got, want)
	}
}

func TestProvider_Login_Success(t *testing.T) {
	var gotBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/orchestration/Authentication/Login" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding login body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := New("user@example.com", "hunter2")
	p.baseURL = server.URL

	if err := p.login(context.Background()); err != nil {
		t.Fatalf("login() error = %v", err)
	}
	if !p.loggedIn {
		t.Error("login() did not set loggedIn = true")
	}
	if gotBody["user"] != "user@example.com" || gotBody["pwd"] != "hunter2" {
		t.Errorf("login body = %+v, want user/pwd fields matching credentials", gotBody)
	}
}

func TestProvider_Login_Failure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	p := New("user", "wrong-password")
	p.baseURL = server.URL

	if err := p.login(context.Background()); err == nil {
		t.Fatal("login() error = nil, want error for 401 response")
	}
	if p.loggedIn {
		t.Error("login() set loggedIn = true after a failed login")
	}
}

func TestProvider_UserAgent_Default(t *testing.T) {
	var gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := New("user", "pass")
	p.baseURL = server.URL

	if err := p.login(context.Background()); err != nil {
		t.Fatalf("login() error = %v", err)
	}
	if gotUA != DefaultUserAgent {
		t.Errorf("User-Agent = %q, want default %q", gotUA, DefaultUserAgent)
	}
}

func TestProvider_UserAgent_Override(t *testing.T) {
	var gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := New("user", "pass")
	p.baseURL = server.URL
	p.UserAgent = "custom-agent/1.0"

	if err := p.login(context.Background()); err != nil {
		t.Fatalf("login() error = %v", err)
	}
	if gotUA != "custom-agent/1.0" {
		t.Errorf("User-Agent = %q, want custom-agent/1.0", gotUA)
	}
}

func TestProvider_Logger_LogsAuthAndURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orchestration/Authentication/Login":
			w.WriteHeader(http.StatusOK)
		case "/data":
			w.Write([]byte(`{"ok":true}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var buf bytes.Buffer
	p := New("user", "pass")
	p.baseURL = server.URL
	p.Logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	if _, err := p.get(context.Background(), "/data", nil); err != nil {
		t.Fatalf("get() error = %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "authenticating") || !strings.Contains(out, "/orchestration/Authentication/Login") {
		t.Errorf("log output = %q, want an authenticating line with the login URL", out)
	}
	if !strings.Contains(out, "authenticated") {
		t.Errorf("log output = %q, want an authenticated confirmation", out)
	}
	if !strings.Contains(out, server.URL+"/data") {
		t.Errorf("log output = %q, want the requested URL", out)
	}
}

func TestProvider_Get_LogsInFirst(t *testing.T) {
	var loginCalls, dataCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orchestration/Authentication/Login":
			loginCalls++
			w.WriteHeader(http.StatusOK)
		case "/data":
			dataCalls++
			w.Write([]byte(`{"ok":true}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	p := New("user", "pass")
	p.baseURL = server.URL

	body, err := p.get(context.Background(), "/data", nil)
	if err != nil {
		t.Fatalf("get() error = %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("get() body = %s, want {\"ok\":true}", body)
	}
	if loginCalls != 1 || dataCalls != 1 {
		t.Errorf("loginCalls = %d, dataCalls = %d, want 1, 1", loginCalls, dataCalls)
	}
}

func TestProvider_Get_RetriesOnceOn401(t *testing.T) {
	var loginCalls, dataCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orchestration/Authentication/Login":
			loginCalls++
			w.WriteHeader(http.StatusOK)
		case "/data":
			dataCalls++
			if dataCalls == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Write([]byte(`{"ok":true}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	p := New("user", "pass")
	p.baseURL = server.URL
	p.loggedIn = true // simulate a stale but not-yet-known-expired session

	body, err := p.get(context.Background(), "/data", nil)
	if err != nil {
		t.Fatalf("get() error = %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("get() body = %s, want {\"ok\":true}", body)
	}
	if loginCalls != 1 || dataCalls != 2 {
		t.Errorf("loginCalls = %d, dataCalls = %d, want 1, 2", loginCalls, dataCalls)
	}
}

func TestProvider_Get_ErrorsOnPersistentFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orchestration/Authentication/Login":
			w.WriteHeader(http.StatusOK)
		case "/data":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	p := New("user", "pass")
	p.baseURL = server.URL

	if _, err := p.get(context.Background(), "/data", nil); err == nil {
		t.Fatal("get() error = nil, want error for persistent 500")
	}
}

func TestProvider_ListPoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/orchestration/Authentication/Login":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/orchestration/User/GetMeteringPointsByBusinesspartnerId" && r.URL.Query().Get("context") == "2":
			w.Write([]byte(`[
				{"meteringPointId": "AT0020000000000000000000100123456", "typeOfRelation": "Bezug", "communicative": true, "locked": false},
				{"meteringPointId": "AT0020000000000000000000100654321", "typeOfRelation": "Einspeisung", "communicative": false, "locked": false},
				{"meteringPointId": "AT0020000000000000000000100999999", "typeOfRelation": "Bezug", "communicative": true, "locked": true}
			]`))
		case r.URL.Path == "/orchestration/User/GetMeteringPointsByBusinesspartnerId" && r.URL.Query().Get("context") == "5":
			w.Write([]byte(`[
				{"meteringPointId": "AT0020000000000000000000100123456", "typeOfRelation": "Bezug", "communicative": true, "locked": false}
			]`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	p := New("user", "pass")
	p.baseURL = server.URL

	points, err := p.ListPoints(context.Background())
	if err != nil {
		t.Fatalf("ListPoints() error = %v", err)
	}
	want := []provider.Point{
		{ID: "AT0020000000000000000000100123456", Name: "Bezug"},
	}
	if len(points) != len(want) || points[0] != want[0] {
		t.Errorf("ListPoints() = %+v, want %+v (non-communicative and locked points excluded; duplicate across contexts deduplicated)", points, want)
	}
}

func TestProvider_FetchDay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orchestration/Authentication/Login":
			w.WriteHeader(http.StatusOK)
		case "/orchestration/ConsumptionRecord/Day":
			if got := r.URL.Query().Get("meterId"); got != "AT0020000000000000000000100123456" {
				t.Fatalf("meterId = %q, want AT0020000000000000000000100123456", got)
			}
			if got := r.URL.Query().Get("day"); got != "2024-01-15" {
				t.Fatalf("day = %q, want 2024-01-15 (zero-padded)", got)
			}
			w.Write([]byte(`[
				{
					"ec_id": "some-community",
					"meteredValues": [99.0]
				},
				{
					"ec_id": null,
					"meteredValues": [1.0, null, null],
					"estimatedValues": [null, 2.0, null],
					"estimatedQualities": [null, "L3", null]
				}
			]`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	p := New("user", "pass")
	p.baseURL = server.URL

	day := time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC)
	readings, err := p.FetchDay(context.Background(), "AT0020000000000000000000100123456", day)
	if err != nil {
		t.Fatalf("FetchDay() error = %v", err)
	}

	if len(readings) != 2 {
		t.Fatalf("FetchDay() returned %d readings, want 2 (null/null interval skipped)", len(readings))
	}

	// Vienna is UTC+1 in January (no DST): local midnight 2024-01-15T00:00 CET
	// = 2024-01-14T23:00:00Z.
	wantFirst := time.Date(2024, time.January, 14, 23, 0, 0, 0, time.UTC)
	if !readings[0].Timestamp.Equal(wantFirst) {
		t.Errorf("readings[0].Timestamp = %v, want %v", readings[0].Timestamp, wantFirst)
	}
	if readings[0].Value != 1000 {
		t.Errorf("readings[0].Value = %v, want 1000 (1.0 kWh metered)", readings[0].Value)
	}
	if readings[0].Quality != "" {
		t.Errorf("readings[0].Quality = %q, want empty (measured values don't get a quality code, see dayRecord's ponytail note)", readings[0].Quality)
	}

	wantSecond := wantFirst.Add(15 * time.Minute)
	if !readings[1].Timestamp.Equal(wantSecond) {
		t.Errorf("readings[1].Timestamp = %v, want %v", readings[1].Timestamp, wantSecond)
	}
	if readings[1].Value != 2000 {
		t.Errorf("readings[1].Value = %v, want 2000 (2.0 kWh estimated fallback)", readings[1].Value)
	}
	if readings[1].Quality != "L3" {
		t.Errorf("readings[1].Quality = %q, want L3 (from estimatedQualities)", readings[1].Quality)
	}
}

func TestProvider_FetchDay_NoTotalRecord(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orchestration/Authentication/Login":
			w.WriteHeader(http.StatusOK)
		case "/orchestration/ConsumptionRecord/Day":
			w.Write([]byte(`[{"ec_id": "some-community", "meteredValues": [1.0]}]`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	p := New("user", "pass")
	p.baseURL = server.URL

	_, err := p.FetchDay(context.Background(), "some-point", time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("FetchDay() error = nil, want error when no ec_id:null record is present")
	}
}

func TestProvider_FetchDay_EstimatedOnlyIntervals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orchestration/Authentication/Login":
			w.WriteHeader(http.StatusOK)
		case "/orchestration/ConsumptionRecord/Day":
			// MeteredValues: [1.0, 2.0], EstimatedValues: [null, null, 3.0, 4.0]
			// Index 0,1: metered wins. Index 2,3: only estimated.
			w.Write([]byte(`[
				{
					"ec_id": null,
					"meteredValues": [1.0, 2.0],
					"estimatedValues": [null, null, 3.0, 4.0]
				}
			]`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	p := New("user", "pass")
	p.baseURL = server.URL

	day := time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC)
	readings, err := p.FetchDay(context.Background(), "some-point", day)
	if err != nil {
		t.Fatalf("FetchDay() error = %v", err)
	}

	if len(readings) != 4 {
		t.Fatalf("FetchDay() returned %d readings, want 4 (includes estimated-only intervals)", len(readings))
	}

	// Vienna is UTC+1 in January: local midnight 2024-01-15T00:00 CET = 2024-01-14T23:00:00Z
	wantBase := time.Date(2024, time.January, 14, 23, 0, 0, 0, time.UTC)

	// Index 0: metered 1.0 kWh
	if !readings[0].Timestamp.Equal(wantBase) {
		t.Errorf("readings[0].Timestamp = %v, want %v", readings[0].Timestamp, wantBase)
	}
	if readings[0].Value != 1000 {
		t.Errorf("readings[0].Value = %v, want 1000", readings[0].Value)
	}

	// Index 1: metered 2.0 kWh
	want1 := wantBase.Add(15 * time.Minute)
	if !readings[1].Timestamp.Equal(want1) {
		t.Errorf("readings[1].Timestamp = %v, want %v", readings[1].Timestamp, want1)
	}
	if readings[1].Value != 2000 {
		t.Errorf("readings[1].Value = %v, want 2000", readings[1].Value)
	}

	// Index 2: estimated only (metered is nil) 3.0 kWh
	want2 := wantBase.Add(30 * time.Minute)
	if !readings[2].Timestamp.Equal(want2) {
		t.Errorf("readings[2].Timestamp = %v, want %v", readings[2].Timestamp, want2)
	}
	if readings[2].Value != 3000 {
		t.Errorf("readings[2].Value = %v, want 3000 (estimated fallback beyond metered length)", readings[2].Value)
	}
	if readings[2].Quality != "" {
		t.Errorf("readings[2].Quality = %q, want empty (no estimatedQualities in response)", readings[2].Quality)
	}

	// Index 3: estimated only 4.0 kWh
	want3 := wantBase.Add(45 * time.Minute)
	if !readings[3].Timestamp.Equal(want3) {
		t.Errorf("readings[3].Timestamp = %v, want %v", readings[3].Timestamp, want3)
	}
	if readings[3].Value != 4000 {
		t.Errorf("readings[3].Value = %v, want 4000", readings[3].Value)
	}
}

func TestProvider_FetchDay_DSTFallBack(t *testing.T) {
	// 2024-10-27 is the fall-back transition day in Europe/Vienna:
	// 03:00 CEST becomes 02:00 CET, so the day has 25 hours = 100 quarter-hour intervals.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orchestration/Authentication/Login":
			w.WriteHeader(http.StatusOK)
		case "/orchestration/ConsumptionRecord/Day":
			if got := r.URL.Query().Get("day"); got != "2024-10-27" {
				t.Fatalf("day = %q, want 2024-10-27", got)
			}
			// Build response with 100 metered values (one for each 15-minute interval).
			meteredValues := make([]*float64, 100)
			val := 1.0
			for i := range meteredValues {
				meteredValues[i] = &val
			}
			resp := []dayRecord{
				{
					ECID:    nil,
					Metered: meteredValues,
				},
			}
			b, _ := json.Marshal(resp)
			w.Write(b)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	p := New("user", "pass")
	p.baseURL = server.URL

	day := time.Date(2024, time.October, 27, 0, 0, 0, 0, time.UTC)
	readings, err := p.FetchDay(context.Background(), "some-point", day)
	if err != nil {
		t.Fatalf("FetchDay() error = %v", err)
	}

	if len(readings) != 100 {
		t.Fatalf("FetchDay() returned %d readings, want 100 (DST day has 25 hours)", len(readings))
	}

	// Vienna is UTC+2 before the fall-back on 2024-10-27.
	// Local midnight 2024-10-27T00:00 CEST = 2024-10-26T22:00:00Z.
	wantFirst := time.Date(2024, time.October, 26, 22, 0, 0, 0, time.UTC)
	if !readings[0].Timestamp.Equal(wantFirst) {
		t.Errorf("readings[0].Timestamp = %v, want %v", readings[0].Timestamp, wantFirst)
	}

	// Verify all readings are exactly 15 minutes apart.
	for i := 1; i < len(readings); i++ {
		prev := readings[i-1].Timestamp
		curr := readings[i].Timestamp
		want := prev.Add(15 * time.Minute)
		if !curr.Equal(want) {
			t.Errorf("readings[%d].Timestamp = %v, want %v (15 min after readings[%d])",
				i, curr, want, i-1)
		}
	}
}
