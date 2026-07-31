// Command smartmeter-fetch fetches smart meter readings from grid operator
// web portals and persists them as one JSON file per provider/point/day
// (internal/store/jsonfile). The query API (internal/api) is not yet
// implemented, so this CLI only covers fetch/store for now.
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
	"strconv"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/config"
	"github.com/welworx/smartmeter-fetch/internal/provider"
	"github.com/welworx/smartmeter-fetch/internal/provider/evn"
	"github.com/welworx/smartmeter-fetch/internal/store"
	"github.com/welworx/smartmeter-fetch/internal/store/jsonfile"
)

var version = "dev"

const usageHeader = `smartmeter-fetch fetches quarter-hourly smart meter readings from grid
operator web portals.

Usage:
  smartmeter-fetch <command> [flags]

Commands:
  list-points   List metering points visible to the account
  fetch         Fetch and persist readings (default: yesterday, every point
                of every configured profile)
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
  SMARTMETER_DATA_DIR     Directory fetch persists readings under (default: ./data).
                          Same as -data-dir; -data-dir wins if both are set.

Examples:
  # Credentials via env vars (recommended: keeps them out of shell history)
  export SMARTMETER_USER=you@example.com
  export SMARTMETER_PASSWORD=hunter2
  smartmeter-fetch list-points

  # Or store credentials once, encrypted under a master passphrase
  smartmeter-fetch profile add home
  smartmeter-fetch fetch -point AT0020000000000000000000100123456 -day 2024-01-15

  # No -point/-day/-profile: fetch yesterday for every point of every
  # stored profile, persisting to ./data
  smartmeter-fetch fetch

  # Fetch a date range instead of a single day
  smartmeter-fetch fetch -point AT0020000000000000000000100123456 -from 2024-01-01 -to 2024-01-31

  # -from/-to also accept a number of days before today, e.g. 30 days ago
  # through 20 days ago; omitting -to defaults it to today
  smartmeter-fetch fetch -point AT0020000000000000000000100123456 -from -30 -to -20

  # Resume each point from its last stored day through yesterday (falls
  # back to just yesterday the first time, before anything is stored);
  # days already stored are skipped, not re-fetched — the intended way to
  # run this on a schedule (cron, systemd timer, ...)
  smartmeter-fetch fetch -since-latest

  # A day already in -data-dir is skipped by default; -force re-fetches
  # and overwrites it (e.g. to pick up a portal revision)
  smartmeter-fetch fetch -point AT0020000000000000000000100123456 -day 2024-01-15 -force

  # Also print the fetch results as JSON to stdout (default: only logged)
  smartmeter-fetch fetch -point AT0020000000000000000000100123456 -day 2024-01-15 -json

  # Debug logging (auth events + request URLs)
  smartmeter-fetch fetch -point AT0020000000000000000000100123456 -day 2024-01-15 -log-level debug

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
// shares: which provider, credentials, User-Agent, and log level.
type providerFlags struct {
	name      string
	user      string
	password  string
	profile   string
	userAgent string
	logLevel  string
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
	fs.StringVar(&c.logLevel, "log-level", "info", "log level: debug (also logs request URLs and auth events), info, warn, or error")
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

// parseLogLevel validates a -log-level value against the names accepted by
// slog.Level.UnmarshalText ("debug", "info", "warn", "error"; case
// insensitive).
func parseLogLevel(s string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return 0, errors.New("must be one of debug, info, warn, error")
	}
	return level, nil
}

func newLogger(level slog.Level, w io.Writer) *slog.Logger {
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

	level, err := parseLogLevel(c.logLevel)
	if err != nil {
		fmt.Fprintf(stderr, "smartmeter-fetch: -log-level %q: %v\n", c.logLevel, err)
		fs.Usage()
		return 2
	}
	log := newLogger(level, stderr)
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

// defaultDataDir is the -data-dir default: $SMARTMETER_DATA_DIR if set,
// else "./data" (relative to the working directory the CLI is run from).
func defaultDataDir() string {
	if d := os.Getenv("SMARTMETER_DATA_DIR"); d != "" {
		return d
	}
	return "data"
}

// fetchFlags holds the flags unique to "fetch" (on top of the common
// providerFlags).
type fetchFlags struct {
	point       string
	day         string
	from        string
	to          string
	sinceLatest bool
	dataDir     string
	printJSON   bool
	force       bool
}

// registerFetchOnlyFlags registers fetchFlags on fs. A standalone function
// so printUsage can list these without also pulling in the common ones a
// second time.
func registerFetchOnlyFlags(fs *flag.FlagSet) *fetchFlags {
	f := &fetchFlags{}
	fs.StringVar(&f.point, "point", "", "metering point ID (default: every point of -profile, or of every configured profile if -profile is also omitted; see list-points)")
	fs.StringVar(&f.day, "day", "", "date to fetch, YYYY-MM-DD (default: yesterday). Mutually exclusive with -from/-to and -since-latest")
	fs.StringVar(&f.from, "from", "", "start of an inclusive date range to fetch: YYYY-MM-DD, or a negative number of days before today, e.g. -30 (see -to)")
	fs.StringVar(&f.to, "to", "", "end of an inclusive date range to fetch: YYYY-MM-DD, or a negative number of days before today, e.g. -20 (default: today, if -from is set)")
	fs.BoolVar(&f.sinceLatest, "since-latest", false, "for each point, fetch from its last stored day through yesterday (falls back to just yesterday if nothing is stored yet). Mutually exclusive with -day and -from/-to")
	fs.StringVar(&f.dataDir, "data-dir", defaultDataDir(), "directory readings are persisted under, one JSON file per provider/point/day (default: $SMARTMETER_DATA_DIR, or \"data\")")
	fs.BoolVar(&f.printJSON, "json", false, "also print fetched results as JSON to stdout (default: only logged; readings are always persisted to -data-dir)")
	fs.BoolVar(&f.force, "force", false, "re-fetch and overwrite days already present in -data-dir (default: skip a day once it's stored, since the portal may still revise it, pass -force to re-check)")
	return f
}

// newFetchFlagSet builds the "fetch" flag set. Shared between runFetch
// (which parses real args) and printUsage (which only wants the flag
// list), so the two can never fall out of sync.
func newFetchFlagSet(out io.Writer) (fs *flag.FlagSet, c *providerFlags, f *fetchFlags) {
	fs = flag.NewFlagSet("fetch", flag.ContinueOnError)
	fs.SetOutput(out)
	c = &providerFlags{}
	c.register(fs)
	f = registerFetchOnlyFlags(fs)
	fs.Usage = func() {
		fmt.Fprint(out, "Fetch smart meter readings for a metering point and persist them.\n\nUsage:\n  smartmeter-fetch fetch [-point <id>] [-day <YYYY-MM-DD> | -from <YYYY-MM-DD|-N> [-to <YYYY-MM-DD|-N>] | -since-latest] [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	return fs, c, f
}

const dayLayout = "2006-01-02"

func parseDay(s string) (time.Time, error) { return time.Parse(dayLayout, s) }

func yesterday() time.Time { return time.Now().AddDate(0, 0, -1) }

// parseDayOrOffset parses s as either an absolute YYYY-MM-DD date or, if it's
// a plain integer, a number of days relative to today (e.g. "-30" for 30
// days ago) — used by -from/-to so a range can be given relative to "now"
// without computing dates by hand.
func parseDayOrOffset(s string) (time.Time, error) {
	if n, err := strconv.Atoi(s); err == nil {
		return time.Now().AddDate(0, 0, n), nil
	}
	return parseDay(s)
}

// fetchPlan is the validated, parsed form of fetchFlags' mutually exclusive
// day-selection flags (-day, -from/-to, -since-latest).
type fetchPlan struct {
	mode          string // "day", "range", or "since-latest"
	day, from, to time.Time
}

// parseFetchPlan validates f's day-selection flags and parses any dates
// among them. It does not resolve -since-latest against the store — that
// depends on the point being fetched, so it happens per-point in days.
func parseFetchPlan(f *fetchFlags) (fetchPlan, error) {
	set := 0
	for _, on := range []bool{f.day != "", f.from != "" || f.to != "", f.sinceLatest} {
		if on {
			set++
		}
	}
	if set > 1 {
		return fetchPlan{}, errors.New("-day, -from/-to, and -since-latest are mutually exclusive")
	}

	switch {
	case f.sinceLatest:
		return fetchPlan{mode: "since-latest"}, nil
	case f.from != "" || f.to != "":
		if f.from == "" {
			return fetchPlan{}, errors.New("-to requires -from")
		}
		from, err := parseDayOrOffset(f.from)
		if err != nil {
			return fetchPlan{}, fmt.Errorf("-from %q: %w", f.from, err)
		}
		to := time.Now()
		if f.to != "" {
			to, err = parseDayOrOffset(f.to)
			if err != nil {
				return fetchPlan{}, fmt.Errorf("-to %q: %w", f.to, err)
			}
		}
		if to.Before(from) {
			return fetchPlan{}, fmt.Errorf("-to %s is before -from %s", f.to, f.from)
		}
		return fetchPlan{mode: "range", from: from, to: to}, nil
	default:
		dayStr := f.day
		if dayStr == "" {
			dayStr = yesterday().Format(dayLayout)
		}
		day, err := parseDay(dayStr)
		if err != nil {
			return fetchPlan{}, fmt.Errorf("-day %q: %w", dayStr, err)
		}
		return fetchPlan{mode: "day", day: day}, nil
	}
}

// days resolves p into the concrete, ascending list of days to fetch for
// one provider/point. Only "since-latest" needs st and providerName/pointID
// — it looks up the point's last stored day (see CLAUDE.md: never assume
// "yesterday" is complete, always resume from the last consumed point).
func (p fetchPlan) days(ctx context.Context, st store.Store, providerName, pointID string) ([]time.Time, error) {
	switch p.mode {
	case "range":
		return dayRange(p.from, p.to), nil
	case "since-latest":
		y := yesterday()
		latest, found, err := st.Latest(ctx, providerName, pointID)
		if err != nil {
			return nil, err
		}
		if !found {
			return []time.Time{y}, nil
		}
		return dayRange(latest, y), nil
	default: // "day"
		return []time.Time{p.day}, nil
	}
}

func dayRange(from, to time.Time) []time.Time {
	var days []time.Time
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		days = append(days, d)
	}
	return days
}

func runFetch(args []string, stdout, stderr io.Writer) int {
	fs, c, f := newFetchFlagSet(stderr)
	if err := fs.Parse(args); err != nil {
		return exitCodeForParseErr(err)
	}

	plan, err := parseFetchPlan(f)
	if err != nil {
		fmt.Fprintf(stderr, "smartmeter-fetch: %v\n", err)
		fs.Usage()
		return 2
	}
	level, err := parseLogLevel(c.logLevel)
	if err != nil {
		fmt.Fprintf(stderr, "smartmeter-fetch: -log-level %q: %v\n", c.logLevel, err)
		fs.Usage()
		return 2
	}
	log := newLogger(level, stderr)
	st := jsonfile.New(f.dataDir)

	if f.point != "" {
		profiles, err := c.resolveProfiles()
		if err != nil {
			fmt.Fprintf(stderr, "smartmeter-fetch: %v\n", err)
			return 2
		}
		prof := profiles[0]
		p, err := newProviderFor(prof.providerName, prof.user, prof.password, c.userAgent, log)
		if err != nil {
			fmt.Fprintf(stderr, "smartmeter-fetch: %v\n", err)
			return 2
		}
		results, err := fetchPointDays(context.Background(), p, st, plan, f.force, prof.label, f.point, "", log)
		if err != nil {
			fmt.Fprintf(stderr, "smartmeter-fetch: %v\n", err)
			return 1
		}
		return printFetchResults(stdout, stderr, results, f.printJSON)
	}

	return runFetchAll(c, plan, st, f.force, log, f.printJSON, stdout, stderr)
}

// fetchResult is one metering point's fetch outcome.
type fetchResult struct {
	Profile   string `json:"profile,omitempty"`
	Provider  string `json:"provider"`
	Point     string `json:"point,omitempty"`
	PointName string `json:"point_name,omitempty"`
	Day       string `json:"day"`
	Unit      string `json:"unit"`
	// FetchedAt is when this fetch attempt ran, in case the portal later
	// revises "day" (see CLAUDE.md: delayed/amendable data). Zero for a
	// skipped day (see Skipped) — nothing was actually fetched.
	FetchedAt time.Time          `json:"fetched_at,omitzero"`
	Readings  []provider.Reading `json:"readings,omitempty"`
	// Skipped is true when day was already stored and -force wasn't set,
	// so it was left as-is rather than re-fetched.
	Skipped bool   `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

// fetchPointResult fetches one point's day, persists the readings to st on
// success, and wraps the outcome (success, error, or skip) in a
// fetchResult, logging either way. Unless force is set, a day already
// present in st is left alone — the portal may still revise it later, so
// re-checking is opt-in (see the -force flag) rather than automatic.
// Shared by the explicit -point path and runFetchAll's per-point loop, via
// fetchPointDays.
func fetchPointResult(ctx context.Context, p provider.Provider, st store.Store, force bool, profileLabel, pointID, pointName string, day time.Time, log *slog.Logger) fetchResult {
	dayStr := day.Format(dayLayout)
	res := fetchResult{Profile: profileLabel, Provider: p.Name(), Point: pointID, PointName: pointName, Day: dayStr, Unit: provider.Unit}

	if !force {
		has, err := st.Has(ctx, p.Name(), pointID, day)
		if err != nil {
			log.Error("checking stored data failed", "profile", profileLabel, "point", pointID, "day", dayStr, "error", err)
			res.Error = err.Error()
			return res
		}
		if has {
			log.Info("day already stored, skipping (use -force to refetch)", "profile", profileLabel, "point", pointID, "day", dayStr)
			res.Skipped = true
			return res
		}
	}

	log.Info("fetching day", "provider", p.Name(), "profile", profileLabel, "point", pointID, "day", dayStr)
	readings, err := p.FetchDay(ctx, pointID, day)
	res.FetchedAt = time.Now().UTC()
	if err != nil {
		log.Error("fetching day failed", "profile", profileLabel, "point", pointID, "error", err)
		res.Error = err.Error()
		return res
	}
	res.Readings = readings
	log.Debug("fetched readings", "profile", profileLabel, "point", pointID, "count", len(readings))

	if err := st.Put(ctx, p.Name(), pointID, readings); err != nil {
		log.Error("storing readings failed", "profile", profileLabel, "point", pointID, "day", dayStr, "error", err)
		res.Error = err.Error()
		return res
	}
	log.Info("stored readings", "profile", profileLabel, "point", pointID, "day", dayStr, "count", len(readings))
	return res
}

// fetchPointDays resolves plan into concrete days for one provider/point
// and fetches (and persists) each in order.
func fetchPointDays(ctx context.Context, p provider.Provider, st store.Store, plan fetchPlan, force bool, profileLabel, pointID, pointName string, log *slog.Logger) ([]fetchResult, error) {
	days, err := plan.days(ctx, st, p.Name(), pointID)
	if err != nil {
		return nil, fmt.Errorf("resolving days to fetch for %s: %w", pointID, err)
	}
	results := make([]fetchResult, 0, len(days))
	for _, day := range days {
		results = append(results, fetchPointResult(ctx, p, st, force, profileLabel, pointID, pointName, day, log))
	}
	return results, nil
}

// printFetchResults optionally prints results as JSON (when printJSONOutput
// is set — see the -json flag) and derives the exit code: 1 if any result
// carries an error, 0 otherwise (2 is reserved for usage/setup errors,
// returned by callers before this point).
func printFetchResults(stdout, stderr io.Writer, results []fetchResult, printJSONOutput bool) int {
	if printJSONOutput {
		if code := printJSON(stdout, stderr, results); code != 0 {
			return code
		}
	}
	for _, r := range results {
		if r.Error != "" {
			return 1
		}
	}
	return 0
}

// runFetchAll fetches plan's days for every point of every profile resolved
// by c (see providerFlags.resolveProfiles), continuing past a failed
// profile or point so one bad login or point doesn't block the rest.
func runFetchAll(c *providerFlags, plan fetchPlan, st store.Store, force bool, log *slog.Logger, printJSONOutput bool, stdout, stderr io.Writer) int {
	profiles, err := c.resolveProfiles()
	if err != nil {
		fmt.Fprintf(stderr, "smartmeter-fetch: %v\n", err)
		return 2
	}

	var results []fetchResult
	for _, prof := range profiles {
		p, err := newProviderFor(prof.providerName, prof.user, prof.password, c.userAgent, log)
		if err != nil {
			fmt.Fprintf(stderr, "smartmeter-fetch: %v\n", err)
			results = append(results, fetchResult{Profile: prof.label, Provider: prof.providerName, Unit: provider.Unit, Error: err.Error()})
			continue
		}

		log.Info("listing metering points", "provider", p.Name(), "profile", prof.label)
		points, err := p.ListPoints(context.Background())
		if err != nil {
			log.Error("listing metering points failed", "profile", prof.label, "error", err)
			results = append(results, fetchResult{Profile: prof.label, Provider: p.Name(), Unit: provider.Unit, Error: err.Error()})
			continue
		}

		for _, pt := range points {
			ptResults, err := fetchPointDays(context.Background(), p, st, plan, force, prof.label, pt.ID, pt.Name, log)
			if err != nil {
				log.Error("resolving fetch range failed", "profile", prof.label, "point", pt.ID, "error", err)
				results = append(results, fetchResult{Profile: prof.label, Provider: p.Name(), Point: pt.ID, PointName: pt.Name, Unit: provider.Unit, Error: err.Error()})
				continue
			}
			results = append(results, ptResults...)
		}
	}

	return printFetchResults(stdout, stderr, results, printJSONOutput)
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
