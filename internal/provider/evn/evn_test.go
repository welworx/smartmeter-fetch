package evn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
