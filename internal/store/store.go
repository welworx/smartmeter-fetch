// Package store defines the interface implemented by each storage backend
// (internal/store/jsonfile, and future backends as siblings).
package store

import (
	"context"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
)

// PointRef identifies one provider/point pair with data currently stored.
type PointRef struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

// Store persists and retrieves readings, keyed by provider name and
// metering point ID.
type Store interface {
	// Put writes readings for one provider/point, bucketed into one
	// day-file per calendar day in loc (the provider's own day-boundary
	// timezone — see provider.Provider.Location). Implementations must
	// be safe to call again for a day that was already written —
	// upstream portals can publish data late, and a later call with the
	// same timestamps must replace, not duplicate, the earlier values.
	Put(ctx context.Context, providerName, pointID string, readings []provider.Reading, loc *time.Location) error

	// Get returns all readings for one provider/point with a timestamp at
	// or after since, ordered by timestamp ascending. Callers resume from
	// the last timestamp they successfully consumed, not from a fixed
	// offset from "now". Unlike Put/Latest/Has, Get has no day-bucketing
	// concept — since is an instant, not a calendar day — so it takes no
	// location.
	Get(ctx context.Context, providerName, pointID string, since time.Time) ([]provider.Reading, error)

	// Latest returns the day (in loc) of the most recent readings stored
	// for one provider/point, so a caller can resume fetching from there
	// instead of from a fixed offset from "now". found is false if
	// nothing has been stored yet for that provider/point.
	Latest(ctx context.Context, providerName, pointID string, loc *time.Location) (day time.Time, found bool, err error)

	// Has reports whether readings are already stored for one
	// provider/point on one day (in loc), so a caller can skip a
	// redundant fetch.
	Has(ctx context.Context, providerName, pointID string, day time.Time, loc *time.Location) (bool, error)

	// ListPoints returns every provider/point pair with data currently
	// stored, discovered from the store itself rather than the portal —
	// so it works without credentials (e.g. for a read-only query
	// server). Results are not required to be sorted by callers, but
	// jsonfile.Store returns them sorted by provider then ID.
	ListPoints(ctx context.Context) ([]PointRef, error)
}
