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

	"github.com/welworx/smartmeter-fetch/internal/config"
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
  fetch         Fetch one day's readings (default: yesterday, every point of
                every configured profile)
  profile       Manage stored portal credentials (add/list/update/verify/remove/passphrase)
  version       Print version and exit
  help          Print this message
`

const usageFooter = `
Environment variables:
  SMARTMETER_USER         Portal username. Same as -user; -user wins if both are set.
  SMARTMETER_PASSWORD     Portal password. Same as -password; -password wins if both are set.
  SMARTMETER_PASSPHRASE   credentials.enc master passphrase, skips the interactive prompt
                          (used by "profile" commands and, as a fallback, by
                          list-points/fetch when reading a stored -profile)
  SMARTMETER_CONFIG_DIR   Directory holding credentials.enc (default: OS config dir,
                          e.g. ~/Library/Application Support/smartmeter-fetch on macOS)

Examples:
  # Credentials via env vars (recommended: keeps them out of shell history)
  export SMARTMETER_USER=you@example.com
  export SMARTMETER_PASSWORD=hunter2
  smartmeter-fetch list-points

  # Or store credentials once, encrypted under a master passphrase
  smartmeter-fetch profile add home
  smartmeter-fetch fetch -point AT0020000000000000000000100123456 -day 2024-01-15

  # No -point/-day/-profile: fetch yesterday for every point of every
  # stored profile
  smartmeter-fetch fetch

  # Fetch one day's readings, with verbose logging (auth events + request URLs)
  smartmeter-fetch fetch -point AT0020000000000000000000100123456 -day 2024-01-15 -v

  # Override the User-Agent sent to the portal
  smartmeter-fetch fetch -point <id> -day 2024-01-15 -user-agent "my-agent/1.0"

  # Recheck stored credentials are still accepted by the portal
  smartmeter-fetch profile verify
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

	fmt.Fprint(w, "\n")
	printProfileUsage(w)

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
	case "profile":
		return runProfile(rest, stdout, stderr)
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
	profile   string
	userAgent string
	verbose   bool
}

// defaultProviderName is used when a stored profile predates the Provider
// field (empty) and as the default for the -provider flag.
const defaultProviderName = "evn"

func (c *providerFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.name, "provider", defaultProviderName, "grid operator provider (only \"evn\" is currently supported)")
	fs.StringVar(&c.user, "user", os.Getenv("SMARTMETER_USER"), "portal username (default: $SMARTMETER_USER)")
	fs.StringVar(&c.password, "password", os.Getenv("SMARTMETER_PASSWORD"), "portal password (default: $SMARTMETER_PASSWORD)")
	fs.StringVar(&c.profile, "profile", "", "name of a stored profile to use instead of -user/-password/-provider (see: smartmeter-fetch profile add); default: first configured profile")
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
	user, password, providerName, err := c.resolveCredentials()
	if err != nil {
		return nil, err
	}
	return newProviderFor(providerName, user, password, c.userAgent, logger)
}

func newProviderFor(providerName, user, password, userAgent string, logger *slog.Logger) (provider.Provider, error) {
	factory, ok := providerFactories[providerName]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q (only \"evn\" is supported)", providerName)
	}
	return factory(user, password, userAgent, logger), nil
}

// resolvedProfile is one portal login resolved from either -user/-password
// or a stored profile, ready to build a provider from.
type resolvedProfile struct {
	label                        string // profile name, or "" for -user/-password
	user, password, providerName string
}

// resolveCredentials returns the portal username/password/provider to use
// for a single explicit -point fetch: -profile if set, else the first
// configured profile.
func (c *providerFlags) resolveCredentials() (user, password, providerName string, err error) {
	profiles, err := c.resolveProfiles()
	if err != nil {
		return "", "", "", err
	}
	first := profiles[0]
	return first.user, first.password, first.providerName, nil
}

// resolveProfiles returns every portal login -point-less fetch should run
// against. -user/-password (which already default from $SMARTMETER_USER/
// $SMARTMETER_PASSWORD, see register) take priority and yield a single
// unnamed login using -provider. Otherwise profiles are loaded from
// credentials.enc: -profile picks one by name, or if that's empty every
// configured profile is returned — a profile's own stored provider is used
// instead of -provider, since a profile is a specific portal login.
func (c *providerFlags) resolveProfiles() ([]resolvedProfile, error) {
	if c.user != "" && c.password != "" {
		return []resolvedProfile{{"", c.user, c.password, c.name}}, nil
	}
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	if !config.CredentialsExist(dir) {
		return nil, errors.New("missing credentials: pass -user/-password, set SMARTMETER_USER/SMARTMETER_PASSWORD, or add a profile (smartmeter-fetch profile add <name>)")
	}
	pass, err := readPassphrase(false)
	if err != nil {
		return nil, err
	}
	profiles, err := config.LoadSecrets(dir, pass)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, errors.New("no profiles configured (run: smartmeter-fetch profile add <name>)")
	}
	if c.profile != "" {
		for _, p := range profiles {
			if p.Name == c.profile {
				return []resolvedProfile{{p.Name, p.Username, p.Password, providerOrDefault(p.Provider)}}, nil
			}
		}
		return nil, fmt.Errorf("no profile %q (run: smartmeter-fetch profile add %s)", c.profile, c.profile)
	}
	out := make([]resolvedProfile, len(profiles))
	for i, p := range profiles {
		out[i] = resolvedProfile{p.Name, p.Username, p.Password, providerOrDefault(p.Provider)}
	}
	return out, nil
}

// providerOrDefault fills in defaultProviderName for a profile saved before
// the Provider field existed.
func providerOrDefault(p string) string {
	if p == "" {
		return defaultProviderName
	}
	return p
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
	point = fs.String("point", "", "metering point ID (default: every point of -profile, or of every configured profile if -profile is also omitted; see list-points)")
	day = fs.String("day", "", "date to fetch, YYYY-MM-DD (default: yesterday)")
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
		fmt.Fprint(out, "Fetch one day's readings for a metering point.\n\nUsage:\n  smartmeter-fetch fetch [-point <id>] [-day <YYYY-MM-DD>] [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	return fs, c, point, day
}

func runFetch(args []string, stdout, stderr io.Writer) int {
	fs, c, point, day := newFetchFlagSet(stderr)
	if err := fs.Parse(args); err != nil {
		return exitCodeForParseErr(err)
	}

	dayStr := *day
	if dayStr == "" {
		dayStr = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	}
	parsedDay, err := time.Parse("2006-01-02", dayStr)
	if err != nil {
		fmt.Fprintf(stderr, "smartmeter-fetch: -day %q: %v\n", dayStr, err)
		fs.Usage()
		return 2
	}

	log := newLogger(c.verbose, stderr)

	if *point == "" {
		return runFetchAll(c, parsedDay, dayStr, log, stdout, stderr)
	}

	p, err := c.newProvider(log)
	if err != nil {
		fmt.Fprintf(stderr, "smartmeter-fetch: %v\n", err)
		return 2
	}

	log.Info("fetching day", "provider", p.Name(), "point", *point, "day", dayStr)
	readings, err := p.FetchDay(context.Background(), *point, parsedDay)
	if err != nil {
		log.Error("fetching day failed", "error", err)
		return 1
	}
	log.Debug("fetched readings", "count", len(readings))

	return printJSON(stdout, stderr, readings)
}

// fetchResult is one point's outcome when "fetch" runs without -point,
// fanning out across every point of one or more profiles.
type fetchResult struct {
	Profile   string             `json:"profile,omitempty"`
	Provider  string             `json:"provider"`
	Point     string             `json:"point,omitempty"`
	PointName string             `json:"point_name,omitempty"`
	Day       string             `json:"day"`
	Unit      string             `json:"unit"`
	Readings  []provider.Reading `json:"readings,omitempty"`
	Error     string             `json:"error,omitempty"`
}

// runFetchAll fetches day for every point of every profile resolved by c
// (see providerFlags.resolveProfiles), continuing past a failed profile or
// point so one bad login or point doesn't block the rest. Exit code is 1 if
// any profile/point failed, even though partial results are still printed.
func runFetchAll(c *providerFlags, day time.Time, dayStr string, log *slog.Logger, stdout, stderr io.Writer) int {
	profiles, err := c.resolveProfiles()
	if err != nil {
		fmt.Fprintf(stderr, "smartmeter-fetch: %v\n", err)
		return 2
	}

	var results []fetchResult
	failed := false
	for _, prof := range profiles {
		p, err := newProviderFor(prof.providerName, prof.user, prof.password, c.userAgent, log)
		if err != nil {
			fmt.Fprintf(stderr, "smartmeter-fetch: %v\n", err)
			failed = true
			continue
		}

		log.Info("listing metering points", "provider", p.Name(), "profile", prof.label)
		points, err := p.ListPoints(context.Background())
		if err != nil {
			log.Error("listing metering points failed", "profile", prof.label, "error", err)
			results = append(results, fetchResult{Profile: prof.label, Provider: p.Name(), Day: dayStr, Unit: provider.Unit, Error: err.Error()})
			failed = true
			continue
		}

		for _, pt := range points {
			log.Info("fetching day", "provider", p.Name(), "profile", prof.label, "point", pt.ID, "day", dayStr)
			res := fetchResult{Profile: prof.label, Provider: p.Name(), Point: pt.ID, PointName: pt.Name, Day: dayStr, Unit: provider.Unit}
			readings, err := p.FetchDay(context.Background(), pt.ID, day)
			if err != nil {
				log.Error("fetching day failed", "profile", prof.label, "point", pt.ID, "error", err)
				res.Error = err.Error()
				failed = true
			} else {
				res.Readings = readings
			}
			results = append(results, res)
		}
	}

	if code := printJSON(stdout, stderr, results); code != 0 {
		return code
	}
	if failed {
		return 1
	}
	return 0
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
