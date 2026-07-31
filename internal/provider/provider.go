// Package provider defines the interface implemented by each grid operator
// integration (internal/provider/evn, and future operators as siblings).
package provider

import (
	"context"
	"time"
)

// Point is a single metering point ("Zählpunkt") as reported by a provider.
// A prosumer account typically has one Point per direction (consumption and
// production/feed-in each get their own Point).
type Point struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Reading is a single interval's energy value, in Unit (see Unit).
type Reading struct {
	// Timestamp is the start of the interval, in UTC.
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	// Quality is the portal's data-quality code for this interval: "L1"
	// (measured, final), "L2" (substitute, final) or "L3" (substitute,
	// provisional - the value may still change; see CLAUDE.md's
	// delayed-data note). Empty if the provider doesn't report quality.
	Quality string `json:"quality,omitempty"`
}

// Unit is the unit of measurement of every Reading.Value, for every
// provider.
const Unit = "Wh"

// Provider fetches readings from one grid operator's web portal.
type Provider interface {
	// Name identifies this provider, e.g. "evn".
	Name() string

	// ListPoints returns the metering points visible to this account.
	ListPoints(ctx context.Context) ([]Point, error)

	// FetchDay returns the readings for one metering point on one day.
	// The portal may only have partial or no data yet for recent days —
	// callers should not assume "yesterday" is complete.
	FetchDay(ctx context.Context, pointID string, day time.Time) ([]Reading, error)
}
