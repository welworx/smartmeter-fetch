// Package jsonfile implements store.Store as one JSON file per
// provider/metering point/day (data/<provider>/<point>/<date>.json),
// written atomically (temp file + rename) so a concurrent reader never
// sees a half-written file when a delayed day gets rewritten.
package jsonfile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/atomicfile"
	"github.com/welworx/smartmeter-fetch/internal/provider"
	"github.com/welworx/smartmeter-fetch/internal/store"
)

const dayFormat = "2006-01-02"

// Store is a store.Store backed by one JSON file per provider/point/day
// under Dir.
type Store struct {
	Dir string
}

// New returns a Store rooted at dir. dir is created on first write, not here.
func New(dir string) *Store {
	return &Store{Dir: dir}
}

func (s *Store) pointDir(providerName, pointID string) string {
	return filepath.Join(s.Dir, providerName, pointID)
}

func (s *Store) dayPath(providerName, pointID string, day time.Time, loc *time.Location) string {
	return filepath.Join(s.pointDir(providerName, pointID), day.In(loc).Format(dayFormat)+".json")
}

// Put writes readings for one provider/point, grouped and replaced one day
// at a time: readings are split by their calendar day in loc (the
// provider's own day-boundary timezone — every reading from a single
// FetchDay call lands in exactly one file this way, since they were all
// constructed from that same provider-local midnight), and each day's
// file is overwritten wholesale, so calling Put again for an
// already-stored day (the portal republishing a delayed/revised day)
// replaces it rather than duplicating entries.
func (s *Store) Put(ctx context.Context, providerName, pointID string, readings []provider.Reading, loc *time.Location) error {
	if len(readings) == 0 {
		return nil
	}

	byDay := make(map[string][]provider.Reading)
	for _, r := range readings {
		day := r.Timestamp.In(loc).Format(dayFormat)
		byDay[day] = append(byDay[day], r)
	}

	dir := s.pointDir(providerName, pointID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for day, dayReadings := range byDay {
		sort.Slice(dayReadings, func(i, j int) bool {
			return dayReadings[i].Timestamp.Before(dayReadings[j].Timestamp)
		})
		dayTime, err := time.ParseInLocation(dayFormat, day, loc)
		if err != nil {
			return err
		}
		if err := writeAtomic(s.dayPath(providerName, pointID, dayTime, loc), dayReadings); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, data)
}

// Get returns every stored reading for providerName/pointID with a
// timestamp at or after since, ordered ascending.
//
// ponytail: reads every day file for the point and filters in memory
// rather than pruning by filename first — simplest correct thing for a
// single-user tool's data volume; add filename-based pruning if Get ever
// shows up in a profile.
func (s *Store) Get(ctx context.Context, providerName, pointID string, since time.Time) ([]provider.Reading, error) {
	entries, err := os.ReadDir(s.pointDir(providerName, pointID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []provider.Reading
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var dayReadings []provider.Reading
		data, err := os.ReadFile(filepath.Join(s.pointDir(providerName, pointID), e.Name()))
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &dayReadings); err != nil {
			return nil, err
		}
		for _, r := range dayReadings {
			if !r.Timestamp.Before(since) {
				out = append(out, r)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out, nil
}

// Latest returns the most recent day (in loc) with a stored file for
// providerName/pointID, found from the day filenames (which sort
// lexicographically the same as chronologically) rather than reading and
// parsing every file's contents.
func (s *Store) Latest(ctx context.Context, providerName, pointID string, loc *time.Location) (time.Time, bool, error) {
	entries, err := os.ReadDir(s.pointDir(providerName, pointID))
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, err
	}

	var latest string
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".json")
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if name > latest {
			latest = name
		}
	}
	if latest == "" {
		return time.Time{}, false, nil
	}
	day, err := time.ParseInLocation(dayFormat, latest, loc)
	if err != nil {
		return time.Time{}, false, err
	}
	return day, true, nil
}

// Has reports whether a day file exists for providerName/pointID/day (day
// interpreted in loc).
func (s *Store) Has(ctx context.Context, providerName, pointID string, day time.Time, loc *time.Location) (bool, error) {
	_, err := os.Stat(s.dayPath(providerName, pointID, day, loc))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// ListPoints returns every provider/point pair with data currently
// stored under Dir, discovered from the directory layout itself — no
// portal call, so this works without credentials.
func (s *Store) ListPoints(ctx context.Context) ([]store.PointRef, error) {
	providerEntries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []store.PointRef
	for _, pe := range providerEntries {
		if !pe.IsDir() {
			continue
		}
		pointEntries, err := os.ReadDir(filepath.Join(s.Dir, pe.Name()))
		if err != nil {
			return nil, err
		}
		for _, pointE := range pointEntries {
			if !pointE.IsDir() {
				continue
			}
			out = append(out, store.PointRef{Provider: pe.Name(), ID: pointE.Name()})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
