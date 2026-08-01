package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/api"
	"github.com/welworx/smartmeter-fetch/internal/provider"
	"github.com/welworx/smartmeter-fetch/internal/store/jsonfile"
)

func TestServe_ServesStoredPointsAndShutsDownOnCancel(t *testing.T) {
	dir := t.TempDir()
	st := jsonfile.New(dir)
	if err := st.Put(context.Background(), "evn", "AT001", []provider.Reading{
		{Timestamp: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), Value: 1},
	}, time.UTC); err != nil {
		t.Fatalf("Put: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := api.NewHandler(st, log)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- serve(ctx, ln, handler, log) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/v1/points")
	if err != nil {
		t.Fatalf("GET /v1/points: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("serve() after cancel = %v, want nil (clean shutdown)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve() did not return within 5s of context cancellation")
	}
}

func TestRun_Serve_UnknownLogLevel(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"serve", "-log-level", "bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("run(serve, bad -log-level) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `-log-level "bogus"`) {
		t.Errorf("stderr = %q, want -log-level error message", stderr.String())
	}
}

func TestRun_Serve_ListenErrorReturns1(t *testing.T) {
	// Bind a listener ourselves to occupy a port, then point -addr at it.
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer taken.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"serve", "-addr", taken.Addr().String()}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("run(serve, taken -addr) = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "listen failed") {
		t.Errorf("stderr = %q, want listen failure log", stderr.String())
	}
}
