// get.go

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
	"github.com/welworx/smartmeter-fetch/internal/store"
	"github.com/welworx/smartmeter-fetch/internal/store/jsonfile"
)

// getFlags holds the flags unique to "get": the same day-selection flags
// as "fetch" (point/day/from/to/since-latest/data-dir/force) plus
// output-shaping flags (sample/format/out).
type getFlags struct {
	point       string
	day         string
	from        string
	to          string
	sinceLatest bool
	dataDir     string
	force       bool
	sample      string
	format      string
	out         string
}

func registerGetFlags(fs *flag.FlagSet) *getFlags {
	f := &getFlags{}
	fs.StringVar(&f.point, "point", "", "metering point ID (default: every point of -profile, or of every configured profile if -profile is also omitted; see list-points)")
	fs.StringVar(&f.day, "day", "", "date to get, YYYY-MM-DD (default: yesterday). Mutually exclusive with -from/-to and -since-latest")
	fs.StringVar(&f.from, "from", "", "start of an inclusive date range to get: YYYY-MM-DD, or a negative number of days before today, e.g. -30 (see -to)")
	fs.StringVar(&f.to, "to", "", "end of an inclusive date range to get: YYYY-MM-DD, or a negative number of days before today, e.g. -20 (default: today, if -from is set)")
	fs.BoolVar(&f.sinceLatest, "since-latest", false, "for each point, get from its last stored day through yesterday (falls back to just yesterday if nothing is stored yet). Mutually exclusive with -day and -from/-to")
	fs.StringVar(&f.dataDir, "data-dir", defaultDataDir(), "directory readings are persisted under (default: $SMARTMETER_DATA_DIR, or \"data\")")
	fs.BoolVar(&f.force, "force", false, "re-fetch and overwrite days already present in -data-dir before reading them back")
	fs.StringVar(&f.sample, "sample", string(sampleRaw), "aggregation level: raw, hour, day, week, month, or quarter")
	fs.StringVar(&f.format, "format", "text", "stdout output format: text, json, or csv (ignored if -out is set)")
	fs.StringVar(&f.out, "out", "", "write output to file(s) instead of stdout, using a path template with <profile>, <zaehlerpunkt>, <zaehlerpunkt_id>, <yyyy>, <mm>, <dd> placeholders; extension (.csv or .json) selects the file format. Each rendered file is replaced wholesale on every run, never appended to")
	return f
}

func newGetFlagSet(out io.Writer) (fs *flag.FlagSet, c *providerFlags, f *getFlags) {
	fs = flag.NewFlagSet("get", flag.ContinueOnError)
	fs.SetOutput(out)
	c = &providerFlags{}
	c.register(fs)
	f = registerGetFlags(fs)
	fs.Usage = func() {
		fmt.Fprint(out, "Ensure a date range is fetched, then print or export it, optionally aggregated.\n\nUsage:\n  smartmeter-fetch get [-point <id>] [-day <YYYY-MM-DD> | -from <YYYY-MM-DD|-N> [-to <YYYY-MM-DD|-N>] | -since-latest] [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	return fs, c, f
}

// toFetchPlan reuses fetch's day-selection parsing, since -get takes
// identical flags for it.
func (f *getFlags) toFetchPlan() (fetchPlan, error) {
	ff := &fetchFlags{point: f.point, day: f.day, from: f.from, to: f.to, sinceLatest: f.sinceLatest}
	return parseFetchPlan(ff)
}

func runGet(args []string, stdout, stderr io.Writer) int {
	fs, c, f := newGetFlagSet(stderr)
	if err := fs.Parse(args); err != nil {
		return exitCodeForParseErr(err)
	}

	level, err := resolveLogLevel(c)
	if err != nil {
		fmt.Fprintf(stderr, "smartmeter-fetch: -log-level %q: %v\n", c.logLevel, err)
		return 2
	}
	log := newLogger(level, stderr)

	plan, err := f.toFetchPlan()
	if err != nil {
		log.Error("invalid get flags", "error", err)
		return 2
	}
	sample, err := parseSampleLevel(f.sample)
	if err != nil {
		log.Error("invalid -sample", "error", err)
		return 2
	}
	if f.out == "" {
		if f.format != "text" && f.format != "json" && f.format != "csv" {
			log.Error("invalid -format", "error", fmt.Errorf("must be one of text, json, csv"))
			return 2
		}
	} else if _, err := outFormat(f.out); err != nil {
		log.Error("invalid -out", "error", err)
		return 2
	}

	st := jsonfile.New(f.dataDir)

	profiles, err := c.resolveProfiles()
	if err != nil {
		log.Error("resolving profiles failed", "error", err)
		return 2
	}
	if f.point != "" {
		profiles = profiles[:1]
	}

	var points []pointOutput
	exitCode := 0
	for _, prof := range profiles {
		p, err := newProviderFor(prof.providerName, prof.user, prof.password, c.userAgent, log)
		if err != nil {
			log.Error("creating provider failed", "profile", prof.label, "error", err)
			exitCode = 1
			continue
		}

		var targets []provider.Point
		if f.point != "" {
			targets = []provider.Point{{ID: f.point}}
		} else {
			log.Info("listing metering points", "provider", p.Name(), "profile", prof.label)
			targets, err = p.ListPoints(context.Background())
			if err != nil {
				log.Error("listing metering points failed", "profile", prof.label, "error", err)
				exitCode = 1
				continue
			}
		}

		idx, err := loadPointIndex(f.dataDir, p.Name())
		if err != nil {
			log.Error("loading point index failed", "provider", p.Name(), "error", err)
			exitCode = 1
			continue
		}

		for _, pt := range targets {
			po, err := getPoint(context.Background(), p, st, plan, f.force, sample, prof.label, pt, idx, log)
			if err != nil {
				log.Error("get failed", "profile", prof.label, "point", pt.ID, "error", err)
				exitCode = 1
				continue
			}
			points = append(points, po)
		}
	}
	if exitCode != 0 {
		return exitCode
	}

	if f.out != "" {
		return writeGetOut(f.out, points, log)
	}
	return writeGetStdout(stdout, f.format, points, log)
}

// getPoint ensures plan's days are stored for pt (via fetchPointDays,
// same as "fetch"), reads the resulting range back from st, aggregates it
// per sample, and returns it wrapped for output.
func getPoint(ctx context.Context, p provider.Provider, st store.Store, plan fetchPlan, force bool, sample sampleLevel, profileLabel string, pt provider.Point, idx *pointIndex, log *slog.Logger) (pointOutput, error) {
	results, err := fetchPointDays(ctx, p, st, plan, force, profileLabel, pt.ID, pt.Name, log)
	if err != nil {
		return pointOutput{}, err
	}
	for _, r := range results {
		if r.Error != "" {
			return pointOutput{}, fmt.Errorf("fetching %s on %s: %s", pt.ID, r.Day, r.Error)
		}
	}

	ordinal, err := idx.ordinal(pt.ID)
	if err != nil {
		return pointOutput{}, fmt.Errorf("assigning point index for %s: %w", pt.ID, err)
	}
	out := pointOutput{
		Profile:        profileLabel,
		Provider:       p.Name(),
		Point:          pt.ID,
		PointName:      pt.Name,
		Sample:         string(sample),
		ZaehlerpunktID: ordinal,
		loc:            p.Location(),
	}
	// plan.days can legitimately resolve to nothing (-since-latest once the
	// latest stored day is today or later), in which case there's no range
	// to read back — not an error, just an empty result.
	if len(results) == 0 {
		return out, nil
	}

	// The read-back window comes from the days actually fetched, not from
	// plan's time.Time values: those are UTC (and, for relative -from/-to,
	// carry a wall-clock time of day), while readings are grouped by the
	// provider-local day the provider assigned them to. Parsing the
	// fetched day strings in the provider's own location gives exact
	// midnight-to-midnight bounds.
	from, err := time.ParseInLocation(dayLayout, results[0].Day, p.Location())
	if err != nil {
		return pointOutput{}, fmt.Errorf("parsing day %q: %w", results[0].Day, err)
	}
	lastDay, err := time.ParseInLocation(dayLayout, results[len(results)-1].Day, p.Location())
	if err != nil {
		return pointOutput{}, fmt.Errorf("parsing day %q: %w", results[len(results)-1].Day, err)
	}
	to := lastDay.AddDate(0, 0, 1) // exclusive end: midnight after the last fetched day

	readings, err := st.Get(ctx, p.Name(), pt.ID, from)
	if err != nil {
		return pointOutput{}, fmt.Errorf("reading back %s: %w", pt.ID, err)
	}
	filtered := make([]provider.Reading, 0, len(readings))
	for _, r := range readings {
		if r.Timestamp.Before(to) {
			filtered = append(filtered, r)
		}
	}

	out.Readings = toOutputRows(aggregate(filtered, sample, p.Location()))
	return out, nil
}

func writeGetStdout(stdout io.Writer, format string, points []pointOutput, log *slog.Logger) int {
	switch format {
	case "text":
		if err := writeText(stdout, points); err != nil {
			log.Error("writing text output failed", "error", err)
			return 1
		}
	case "json":
		if err := writeJSON(stdout, points); err != nil {
			log.Error("writing json output failed", "error", err)
			return 1
		}
	case "csv":
		if len(points) != 1 {
			log.Error("csv to stdout requires exactly one point", "error", errors.New("pass -point, or use -out to export multiple points to separate files"))
			return 2
		}
		if err := writeCSV(stdout, points[0].Readings); err != nil {
			log.Error("writing csv output failed", "error", err)
			return 1
		}
	}
	return 0
}

func writeGetOut(tmpl string, points []pointOutput, log *slog.Logger) int {
	// Without a point placeholder every point renders to the same path, so
	// each would silently overwrite the previous one — refuse instead.
	if len(points) > 1 && !strings.Contains(tmpl, "<zaehlerpunkt>") && !strings.Contains(tmpl, "<zaehlerpunkt_id>") {
		log.Error("-out template has no point placeholder but multiple points resolved", "error", errors.New("pass -point, or use -out with <zaehlerpunkt>/<zaehlerpunkt_id> to export multiple points to separate files"))
		return 2
	}
	for _, po := range points {
		vars := pathVars{Profile: po.Profile, Zaehlerpunkt: po.Point, ZaehlerpunktID: po.ZaehlerpunktID}
		if err := writeGroupedOutput(tmpl, vars, po.Readings, po.loc); err != nil {
			log.Error("writing -out file(s) failed", "point", po.Point, "error", err)
			return 1
		}
	}
	return 0
}
