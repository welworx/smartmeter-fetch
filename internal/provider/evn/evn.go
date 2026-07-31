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

//nolint:unused // referenced starting Task 4 (FetchDay)
var viennaLocation = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		panic(fmt.Sprintf("evn: loading Europe/Vienna timezone: %v", err))
	}
	return loc
}()

// Provider fetches readings from the Netz NÖ (EVN) smart meter portal.
type Provider struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
	loggedIn   bool
}

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

type account struct {
	AccountID        string `json:"accountId"`
	HasSmartMeter    bool   `json:"hasSmartMeter"`
	HasElectricity   bool   `json:"hasElectricity"`
	HasCommunicative bool   `json:"hasCommunicative"`
	HasActive        bool   `json:"hasActive"`
}

type meteringPoint struct {
	MeteringPointID string `json:"meteringPointId"`
	TypeOfRelation  string `json:"typeOfRelation"`
}

// ListPoints implements provider.Provider.
func (p *Provider) ListPoints(ctx context.Context) ([]provider.Point, error) {
	body, err := p.get(ctx, "/orchestration/User/GetAccountIdByBussinespartnerId", url.Values{"context": {"2"}})
	if err != nil {
		return nil, err
	}
	var accounts []account
	if err := json.Unmarshal(body, &accounts); err != nil {
		return nil, fmt.Errorf("evn: decoding accounts: %w", err)
	}

	var points []provider.Point
	for _, acc := range accounts {
		if acc.AccountID == "" || !acc.HasSmartMeter || !acc.HasElectricity || !acc.HasCommunicative || !acc.HasActive {
			continue
		}

		body, err := p.get(ctx, "/orchestration/User/GetMeteringPointByAccountId",
			url.Values{"context": {"2"}, "accountId": {acc.AccountID}})
		if err != nil {
			return nil, err
		}
		var meters []meteringPoint
		if err := json.Unmarshal(body, &meters); err != nil {
			return nil, fmt.Errorf("evn: decoding metering points for account %s: %w", acc.AccountID, err)
		}
		for _, m := range meters {
			points = append(points, provider.Point{ID: m.MeteringPointID, Name: m.TypeOfRelation})
		}
	}
	return points, nil
}
