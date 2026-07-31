package evn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		switch r.URL.Path {
		case "/orchestration/Authentication/Login":
			w.WriteHeader(http.StatusOK)
		case "/orchestration/User/GetAccountIdByBussinespartnerId":
			w.Write([]byte(`[
				{"accountId": "acc-eligible", "hasSmartMeter": true, "hasElectricity": true, "hasCommunicative": true, "hasActive": true},
				{"accountId": "acc-ineligible", "hasSmartMeter": false, "hasElectricity": true, "hasCommunicative": true, "hasActive": true}
			]`))
		case "/orchestration/User/GetMeteringPointByAccountId":
			if got := r.URL.Query().Get("accountId"); got != "acc-eligible" {
				t.Fatalf("GetMeteringPointByAccountId called for accountId = %q, want acc-eligible", got)
			}
			w.Write([]byte(`[
				{"meteringPointId": "AT0020000000000000000000100123456", "typeOfRelation": "Verbrauch"}
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
		{ID: "AT0020000000000000000000100123456", Name: "Verbrauch"},
	}
	if len(points) != len(want) || points[0] != want[0] {
		t.Errorf("ListPoints() = %+v, want %+v", points, want)
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
					"estimatedValues": [null, 2.0, null]
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
	if readings[0].ValueWh != 1000 {
		t.Errorf("readings[0].ValueWh = %v, want 1000 (1.0 kWh metered)", readings[0].ValueWh)
	}

	wantSecond := wantFirst.Add(15 * time.Minute)
	if !readings[1].Timestamp.Equal(wantSecond) {
		t.Errorf("readings[1].Timestamp = %v, want %v", readings[1].Timestamp, wantSecond)
	}
	if readings[1].ValueWh != 2000 {
		t.Errorf("readings[1].ValueWh = %v, want 2000 (2.0 kWh estimated fallback)", readings[1].ValueWh)
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
