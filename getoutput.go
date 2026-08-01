// getoutput.go

package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
)

// outputRow is one formatted reading: a provider.Reading plus the
// constant Unit, so text/csv/json output is self-describing rather than
// leaving the unit implicit.
type outputRow struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	Unit      string    `json:"unit"`
	Quality   string    `json:"quality,omitempty"`
}

func toOutputRows(readings []provider.Reading) []outputRow {
	out := make([]outputRow, len(readings))
	for i, r := range readings {
		out[i] = outputRow{Timestamp: r.Timestamp, Value: r.Value, Unit: provider.Unit, Quality: r.Quality}
	}
	return out
}

// pointOutput is one metering point's get result: its identifying
// metadata plus the (possibly aggregated) rows to emit.
type pointOutput struct {
	Profile        string      `json:"profile,omitempty"`
	Provider       string      `json:"provider"`
	Point          string      `json:"point"`
	PointName      string      `json:"point_name,omitempty"`
	Sample         string      `json:"sample"`
	ZaehlerpunktID int         `json:"zaehlerpunkt_id,omitempty"`
	Readings       []outputRow `json:"readings"`
}

// formatValue renders a reading's value for text and CSV output. 'f'
// rather than 'g' so a realistic Wh total (a year's worth easily exceeds
// six significant digits) never flips to scientific notation; shared by
// both writers so the two formats can't drift apart.
func formatValue(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// writeText writes an aligned table per point. A point header line is
// only printed when there's more than one point, so the common
// single-point case stays a plain table.
func writeText(w io.Writer, points []pointOutput) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for i, p := range points {
		if len(points) > 1 {
			if i > 0 {
				fmt.Fprintln(tw)
			}
			fmt.Fprintf(tw, "%s (%s):\n", p.Point, p.PointName)
		}
		fmt.Fprintln(tw, "TIMESTAMP\tVALUE\tUNIT\tQUALITY")
		for _, r := range p.Readings {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Timestamp.Format(time.RFC3339), formatValue(r.Value), r.Unit, r.Quality)
		}
	}
	return tw.Flush()
}

// writeJSON writes points' readings as JSON: a bare array of rows for a
// single point (the common case), or an array of per-point objects when
// more than one point resolved.
func writeJSON(w io.Writer, points []pointOutput) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if len(points) == 1 {
		return enc.Encode(points[0].Readings)
	}
	return enc.Encode(points)
}

// writeCSV writes rows as CSV: a header row, then one row per reading.
// Callers must ensure rows come from exactly one point — CSV has no way
// to disambiguate multiple points in one stream.
func writeCSV(w io.Writer, rows []outputRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"timestamp", "value", "unit", "quality"}); err != nil {
		return err
	}
	for _, r := range rows {
		record := []string{
			r.Timestamp.Format(time.RFC3339),
			formatValue(r.Value),
			r.Unit,
			r.Quality,
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
