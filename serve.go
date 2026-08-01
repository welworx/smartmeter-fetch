// serve.go

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/api"
	"github.com/welworx/smartmeter-fetch/internal/store/jsonfile"
)

const defaultServeAddr = ":8790"

// defaultAddr is the -addr default: $SMARTMETER_ADDR if set, else
// defaultServeAddr.
func defaultAddr() string {
	if a := os.Getenv("SMARTMETER_ADDR"); a != "" {
		return a
	}
	return defaultServeAddr
}

// serveFlags holds the flags for "serve". Deliberately not providerFlags:
// serve never talks to a portal, so it needs no credentials.
type serveFlags struct {
	addr     string
	dataDir  string
	logLevel string
	verbose  bool
}

func registerServeFlags(fs *flag.FlagSet) *serveFlags {
	f := &serveFlags{}
	fs.StringVar(&f.addr, "addr", defaultAddr(), "address to listen on for the /v1 HTTP API (default: $SMARTMETER_ADDR, or \":8790\")")
	fs.StringVar(&f.dataDir, "data-dir", defaultDataDir(), "directory readings are read from, one JSON file per provider/point/day (default: $SMARTMETER_DATA_DIR, or \"data\")")
	fs.StringVar(&f.logLevel, "log-level", "info", "log level: debug, info, warn, or error")
	fs.BoolVar(&f.verbose, "v", false, "shorthand for -log-level debug (wins if both are set)")
	fs.BoolVar(&f.verbose, "verbose", false, "shorthand for -log-level debug (wins if both are set)")
	return f
}

func newServeFlagSet(out io.Writer) (*flag.FlagSet, *serveFlags) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(out)
	f := registerServeFlags(fs)
	fs.Usage = func() {
		fmt.Fprint(out, "Serve stored readings over the /v1 HTTP API.\n\nUsage:\n  smartmeter-fetch serve [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	return fs, f
}

// resolveServeLogLevel is parseLogLevel plus -v/-verbose, mirroring
// resolveLogLevel for providerFlags (serve has its own flag struct since
// it takes no portal credentials).
func resolveServeLogLevel(f *serveFlags) (slog.Level, error) {
	if f.verbose {
		return slog.LevelDebug, nil
	}
	return parseLogLevel(f.logLevel)
}

func runServe(args []string, stdout, stderr io.Writer) int {
	fs, f := newServeFlagSet(stderr)
	if err := fs.Parse(args); err != nil {
		return exitCodeForParseErr(err)
	}

	level, err := resolveServeLogLevel(f)
	if err != nil {
		fmt.Fprintf(stderr, "smartmeter-fetch: -log-level %q: %v\n", f.logLevel, err)
		return 2
	}
	log := newLogger(level, stderr)

	ln, err := net.Listen("tcp", f.addr)
	if err != nil {
		log.Error("listen failed", "addr", f.addr, "error", err)
		return 1
	}
	log.Info("serving /v1 API", "addr", ln.Addr().String(), "data-dir", f.dataDir)

	st := jsonfile.New(f.dataDir)
	handler := api.NewHandler(st, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := serve(ctx, ln, handler, log); err != nil {
		log.Error("serve failed", "error", err)
		return 1
	}
	return 0
}

// serve runs handler on ln until ctx is done, then gracefully shuts down
// (5s deadline). Extracted from runServe so tests can drive it with a
// test-controlled listener and context instead of OS signals and a
// flag-parsed address.
func serve(ctx context.Context, ln net.Listener, handler http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
