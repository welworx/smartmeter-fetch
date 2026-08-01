// pathtemplate.go

package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/atomicfile"
)

// pathVars are the per-point values substituted into a -out path
// template alongside the per-row date placeholders.
type pathVars struct {
	Profile        string
	Zaehlerpunkt   string // the real metering point ID
	ZaehlerpunktID int    // the persisted per-provider ordinal
}

// renderPath substitutes -out template placeholders using vars and t (the
// row's bucket timestamp for the date placeholders). <zaehlerpunkt_id> is
// listed before <zaehlerpunkt> so the longer placeholder is never
// shadowed by a partial match of the shorter one. The date placeholders
// render in Vienna local time, matching the buckets aggregate emits (it
// returns bucket starts as UTC), so a Vienna-day bucket never lands in a
// file named for the previous day, month or year.
func renderPath(tmpl string, vars pathVars, t time.Time) string {
	t = t.In(viennaLocation)
	r := strings.NewReplacer(
		"<zaehlerpunkt_id>", strconv.Itoa(vars.ZaehlerpunktID),
		"<zaehlerpunkt>", vars.Zaehlerpunkt,
		"<profile>", vars.Profile,
		"<yyyy>", t.Format("2006"),
		"<mm>", t.Format("01"),
		"<dd>", t.Format("02"),
	)
	return r.Replace(tmpl)
}

// groupRowsByPath renders tmpl per row's timestamp and groups rows by the
// resulting path — a template containing <yyyy> alone therefore produces
// one file per calendar year, <yyyy>/<mm> one per month, and a template
// with no date placeholder collapses to a single group, with no
// special-casing needed for any particular placeholder combination.
func groupRowsByPath(tmpl string, vars pathVars, rows []outputRow) map[string][]outputRow {
	groups := make(map[string][]outputRow)
	for _, r := range rows {
		path := renderPath(tmpl, vars, r.Timestamp)
		groups[path] = append(groups[path], r)
	}
	return groups
}

// outFormat returns the output format implied by tmpl's file extension.
func outFormat(tmpl string) (string, error) {
	switch {
	case strings.HasSuffix(tmpl, ".csv"):
		return "csv", nil
	case strings.HasSuffix(tmpl, ".json"):
		return "json", nil
	default:
		return "", fmt.Errorf("-out %q: must end in .csv or .json", tmpl)
	}
}

// writeGroupedOutput groups rows by their rendered path (see
// groupRowsByPath) and atomically writes each group to its file, in the
// format implied by tmpl's extension.
func writeGroupedOutput(tmpl string, vars pathVars, rows []outputRow) error {
	format, err := outFormat(tmpl)
	if err != nil {
		return err
	}
	for path, groupRows := range groupRowsByPath(tmpl, vars, rows) {
		var data []byte
		switch format {
		case "csv":
			var buf strings.Builder
			if err := writeCSV(&buf, groupRows); err != nil {
				return fmt.Errorf("formatting %s: %w", path, err)
			}
			data = []byte(buf.String())
		case "json":
			b, err := json.MarshalIndent(groupRows, "", "  ")
			if err != nil {
				return fmt.Errorf("formatting %s: %w", path, err)
			}
			data = b
		}
		if err := atomicfile.WriteFile(path, data); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return nil
}
