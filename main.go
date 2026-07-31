// Command smartmeter-fetch fetches smart meter readings from grid operator
// web portals and prints them as JSON. Storage (internal/store/jsonfile) and
// the query API (internal/api) are not yet implemented, so this CLI only
// covers the fetch side for now.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
	"github.com/welworx/smartmeter-fetch/internal/provider/evn"
)

var version = "dev"

const usageHeader = `smartmeter-fetch fetches quarter-hourly smart meter readings from grid
operator web portals.

Usage:
  smartmeter-fetch <command> [flags]

Commands:
  list-points   List metering points visible to the account
  fetch         Fetch one day's readings for a metering point
  version       Print version and exit
  help          Print this message
`

const usageFooter = `
Environment variables:
  SMARTMETER_USER       Portal username. Same as -user; -user wins if both are set.
  SMARTMETER_PASSWORD   Portal password. Same as -password; -password wins if both are set.

Examples:
  # Credentials via env vars (recommended: keeps them out of shell history)
  export SMARTMETER_USER=you@example.com
  export SMARTMETER_PASSWORD=hunter2
  smartmeter-fetch list-points

  # Fetch one day's readings, with verbose logging (auth events + request URLs)
  smartmeter-fetch fetch -point AT0020000000000000000000100123456 -day 2024-01-15 -v

  # Override the User-Agent sent to the portal
  smartmeter-fetch fetch -point <id> -day 2024-01-15 -user-agent "my-agent/1.0"
`

// printUsage writes the full help text: commands, every flag with its
// default (shared flags listed once, not per subcommand), the environment
// variables that affect behavior, and usage examples. This is the single
// source of truth for help output — "smartmeter-fetch help", "-h"/"--help",
// a bare invocation, and an unknown command all print it, so it can never
// drift out of sync with the flags actually registered below.
func printUsage(w io.Writer) {
	fmt.Fprint(w, usageHeader)

	fmt.Fprint(w, "\nCommon flags (list-points, fetch):\n")
	commonFS := flag.NewFlagSet("common", flag.ContinueOnError)
	commonFS.SetOutput(w)
	var c providerFlags
	c.register(commonFS)
	commonFS.PrintDefaults()

	fmt.Fprint(w, "\nfetch-only flags:\n")
	fetchOnlyFS := flag.NewFlagSet("fetch-only", flag.ContinueOnError)
	fetchOnlyFS.SetOutput(w)
	registerFetchOnlyFlags(fetchOnlyFS)
	fetchOnlyFS.PrintDefaults()

	fmt.Fprint(w, usageFooter)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	case "version":
		fmt.Fprintf(stdout, "smartmeter-fetch %s\n", version)
		return 0
	case "list-points":
		return runListPoints(rest, stdout, stderr)
	case "fetch":
		return runFetch(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "smartmeter-fetch: unknown command %q\n\n", cmd)
		printUsage(stderr)
		return 2
	}
}

// providerFlags registers the flags every provider-talking subcommand
// shares: which provider, credentials, User-Agent, and verbosity.
type providerFlags struct {
	name      string
	user      string
	password  string
	userAgent string
	verbose   bool
}

func (c *providerFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.name, "provider", "evn", "grid operator provider (only \"evn\" is currently supported)")
	fs.StringVar(&c.user, "user", os.Getenv("SMARTMETER_USER"), "portal username (default: $SMARTMETER_USER)")
	fs.StringVar(&c.password, "password", os.Getenv("SMARTMETER_PASSWORD"), "portal password (default: $SMARTMETER_PASSWORD)")
	fs.StringVar(&c.userAgent, "user-agent", "", "User-Agent header sent to the portal (default: a browser-like UA; some portals reject Go's default)")
	fs.BoolVar(&c.verbose, "v", false, "verbose (debug) logging: request URLs and auth events")
	fs.BoolVar(&c.verbose, "verbose", false, "verbose (debug) logging: request URLs and auth events")
}

// providerFactories maps a -provider name to its constructor. A map keeps
// selection and construction in one place and gives tests a seam to inject
// a fake provider without reaching into evn's unexported internals.
var providerFactories = map[string]func(user, password, userAgent string, logger *slog.Logger) provider.Provider{
	"evn": func(user, password, userAgent string, logger *slog.Logger) provider.Provider {
		p := evn.New(user, password)
		p.Logger = logger
		if userAgent != "" {
			p.UserAgent = userAgent
		}
		return p
	},
}

func (c *providerFlags) newProvider(logger *slog.Logger) (provider.Provider, error) {
	if c.user == "" || c.password == "" {
		return nil, errors.New("missing credentials: pass -user/-password or set SMARTMETER_USER/SMARTMETER_PASSWORD")
	}
	factory, ok := providerFactories[c.name]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q (only \"evn\" is supported)", c.name)
	}
	return factory(c.user, c.password, c.userAgent, logger), nil
}

func newLogger(verbose bool, w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// newListPointsFlagSet builds the "list-points" flag set. Shared between
// runListPoints (which parses real args) and printUsage (which only wants
// the flag list), so the two can never fall out of sync.
func newListPointsFlagSet(out io.Writer) (*flag.FlagSet, *providerFlags) {
	fs := flag.NewFlagSet("list-points", flag.ContinueOnError)
	fs.SetOutput(out)
	var c providerFlags
	c.register(fs)
	fs.Usage = func() {
		fmt.Fprint(out, "List metering points visible to the account.\n\nUsage:\n  smartmeter-fetch list-points [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	return fs, &c
}

func runListPoints(args []string, stdout, stderr io.Writer) int {
	fs, c := newListPointsFlagSet(stderr)
	if err := fs.Parse(args); err != nil {
		return exitCodeForParseErr(err)
	}

	log := newLogger(c.verbose, stderr)
	p, err := c.newProvider(log)
	if err != nil {
		fmt.Fprintf(stderr, "smartmeter-fetch: %v\n", err)
		return 2
	}

	log.Info("listing metering points", "provider", p.Name())
	points, err := p.ListPoints(context.Background())
	if err != nil {
		log.Error("listing metering points failed", "error", err)
		return 1
	}
	log.Debug("listed metering points", "count", len(points))

	return printJSON(stdout, stderr, points)
}

// registerFetchOnlyFlags registers the flags unique to "fetch" (on top of
// the common providerFlags). A standalone function so printUsage can list
// these without also pulling in the common ones a second time.
func registerFetchOnlyFlags(fs *flag.FlagSet) (point, day *string) {
	point = fs.String("point", "", "metering point ID (required, see list-points)")
	day = fs.String("day", "", "date to fetch, YYYY-MM-DD (required)")
	return point, day
}

// newFetchFlagSet builds the "fetch" flag set. Shared between runFetch
// (which parses real args) and printUsage (which only wants the flag
// list), so the two can never fall out of sync.
func newFetchFlagSet(out io.Writer) (fs *flag.FlagSet, c *providerFlags, point, day *string) {
	fs = flag.NewFlagSet("fetch", flag.ContinueOnError)
	fs.SetOutput(out)
	c = &providerFlags{}
	c.register(fs)
	point, day = registerFetchOnlyFlags(fs)
	fs.Usage = func() {
		fmt.Fprint(out, "Fetch one day's readings for a metering point.\n\nUsage:\n  smartmeter-fetch fetch -point <id> -day <YYYY-MM-DD> [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	return fs, c, point, day
}

func runFetch(args []string, stdout, stderr io.Writer) int {
	fs, c, point, day := newFetchFlagSet(stderr)
	if err := fs.Parse(args); err != nil {
		return exitCodeForParseErr(err)
	}

	if *point == "" {
		fmt.Fprintln(stderr, "smartmeter-fetch: -point is required")
		fs.Usage()
		return 2
	}
	parsedDay, err := time.Parse("2006-01-02", *day)
	if err != nil {
		fmt.Fprintf(stderr, "smartmeter-fetch: -day %q: %v\n", *day, err)
		fs.Usage()
		return 2
	}

	log := newLogger(c.verbose, stderr)
	p, err := c.newProvider(log)
	if err != nil {
		fmt.Fprintf(stderr, "smartmeter-fetch: %v\n", err)
		return 2
	}

	log.Info("fetching day", "provider", p.Name(), "point", *point, "day", *day)
	readings, err := p.FetchDay(context.Background(), *point, parsedDay)
	if err != nil {
		log.Error("fetching day failed", "error", err)
		return 1
	}
	log.Debug("fetched readings", "count", len(readings))

	return printJSON(stdout, stderr, readings)
}

// exitCodeForParseErr maps a flag.FlagSet.Parse error to a process exit
// code. flag already printed the error and usage via fs.Usage before
// returning it, so there's nothing left to log here.
func exitCodeForParseErr(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}

func printJSON(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(stderr, "smartmeter-fetch: encoding output: %v\n", err)
		return 1
	}
	return 0
}
