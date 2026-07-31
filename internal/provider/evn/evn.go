// Package evn implements provider.Provider for
// https://smartmeter.netz-noe.at/ (Netz NÖ / EVN).
package evn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	_ "time/tzdata"

	"github.com/welworx/smartmeter-fetch/internal/provider"
)

const defaultBaseURL = "https://smartmeter.netz-noe.at"

var viennaLocation = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		panic(fmt.Sprintf("evn: loading Europe/Vienna timezone: %v", err))
	}
	return loc
}()

// Provider fetches readings from the Netz NÖ (EVN) smart meter portal.
// A Provider is not safe for concurrent use.
type Provider struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
	loggedIn   bool
}

var _ provider.Provider = (*Provider)(nil)

// New creates a Provider that authenticates with the given portal
// credentials on first use.
func New(username, password string) *Provider {
	jar, _ := cookiejar.New(nil)
	return &Provider{
		baseURL:    defaultBaseURL,
		username:   username,
		password:   password,
		httpClient: &http.Client{Jar: jar, Timeout: 30 * time.Second},
	}
}

// Name implements provider.Provider.
func (p *Provider) Name() string { return "evn" }

type loginRequest struct {
	User     string `json:"user"`
	Password string `json:"pwd"`
}

func (p *Provider) login(ctx context.Context) error {
	p.loggedIn = false

	body, err := json.Marshal(loginRequest{User: p.username, Password: p.password})
	if err != nil {
		return fmt.Errorf("evn: encoding login request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/orchestration/Authentication/Login", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("evn: building login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("evn: login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("evn: login failed with status %d", resp.StatusCode)
	}
	p.loggedIn = true
	return nil
}

func (p *Provider) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	if !p.loggedIn {
		if err := p.login(ctx); err != nil {
			return nil, err
		}
	}

	body, status, err := p.doGet(ctx, path, query)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized {
		if err := p.login(ctx); err != nil {
			return nil, err
		}
		body, status, err = p.doGet(ctx, path, query)
		if err != nil {
			return nil, err
		}
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("evn: GET %s failed with status %d", path, status)
	}
	return body, nil
}

func (p *Provider) doGet(ctx context.Context, path string, query url.Values) ([]byte, int, error) {
	u := p.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("evn: building request for %s: %w", path, err)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("evn: request %s: %w", path, err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("evn: reading response for %s: %w", path, err)
	}
	return b, resp.StatusCode, nil
}

type meteringPoint struct {
	MeteringPointID string `json:"meteringPointId"`
	TypeOfRelation  string `json:"typeOfRelation"`
	Communicative   bool   `json:"communicative"`
	Locked          bool   `json:"locked"`
}

// meteringPointContexts are the two "context" values the portal's own web
// UI queries for a business partner's metering points. In practice both
// have been observed to return the same set of points for a single-account
// user; querying both and deduplicating is the safe interpretation without
// documentation of what distinguishes them.
var meteringPointContexts = [...]string{"2", "5"}

// ListPoints implements provider.Provider.
func (p *Provider) ListPoints(ctx context.Context) ([]provider.Point, error) {
	seen := make(map[string]bool)
	var points []provider.Point
	for _, ctxVal := range meteringPointContexts {
		body, err := p.get(ctx, "/orchestration/User/GetMeteringPointsByBusinesspartnerId", url.Values{"context": {ctxVal}})
		if err != nil {
			return nil, err
		}
		var meters []meteringPoint
		if err := json.Unmarshal(body, &meters); err != nil {
			return nil, fmt.Errorf("evn: decoding metering points for context %s: %w", ctxVal, err)
		}
		for _, m := range meters {
			if m.MeteringPointID == "" || m.Locked || !m.Communicative || seen[m.MeteringPointID] {
				continue
			}
			seen[m.MeteringPointID] = true
			points = append(points, provider.Point{ID: m.MeteringPointID, Name: m.TypeOfRelation})
		}
	}
	return points, nil
}

type dayRecord struct {
	ECID            *string    `json:"ec_id"`
	MeteredValues   []*float64 `json:"meteredValues"`
	EstimatedValues []*float64 `json:"estimatedValues"`
}

// FetchDay implements provider.Provider.
// FetchDay uses day's calendar date (Year/Month/Day) as-is, in whatever location day's *time.Time carries;
// it does not convert day to Europe/Vienna first.
func (p *Provider) FetchDay(ctx context.Context, pointID string, day time.Time) ([]provider.Reading, error) {
	query := url.Values{
		"meterId": {pointID},
		"day":     {fmt.Sprintf("%04d-%02d-%02d", day.Year(), day.Month(), day.Day())},
	}
	body, err := p.get(ctx, "/orchestration/ConsumptionRecord/Day", query)
	if err != nil {
		return nil, err
	}

	var records []dayRecord
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, fmt.Errorf("evn: decoding day records for point %s: %w", pointID, err)
	}

	var total *dayRecord
	for i := range records {
		if records[i].ECID == nil {
			total = &records[i]
			break
		}
	}
	if total == nil {
		return nil, fmt.Errorf("evn: no total (ec_id: null) record in day response for point %s", pointID)
	}

	midnight := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, viennaLocation)
	maxLen := len(total.MeteredValues)
	if len(total.EstimatedValues) > maxLen {
		maxLen = len(total.EstimatedValues)
	}
	readings := make([]provider.Reading, 0, maxLen)
	for i := 0; i < maxLen; i++ {
		var value *float64
		if i < len(total.MeteredValues) {
			value = total.MeteredValues[i]
		}
		if value == nil && i < len(total.EstimatedValues) {
			value = total.EstimatedValues[i]
		}
		if value == nil {
			continue
		}
		readings = append(readings, provider.Reading{
			Timestamp: midnight.Add(time.Duration(i) * 15 * time.Minute).UTC(),
			ValueWh:   *value * 1000,
		})
	}
	return readings, nil
}
