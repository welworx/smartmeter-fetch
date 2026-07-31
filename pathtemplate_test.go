package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
)

func TestRenderPath_SubstitutesAllPlaceholders(t *testing.T) {
	vars := pathVars{Profile: "home", Zaehlerpunkt: "AT0020000000000000000000100123456", ZaehlerpunktID: 1}
	tmpl := "<profile>/<zaehlerpunkt_id>/<zaehlerpunkt>/<yyyy>/<mm>/<dd>/data.csv"
	got := renderPath(tmpl, vars, time.Date(2024, 3, 7, 0, 0, 0, 0, time.UTC))
	want := "home/1/AT0020000000000000000000100123456/2024/03/07/data.csv"
	if got != want {
		t.Errorf("renderPath = %q, want %q", got, want)
	}
}

func TestRenderPath_ZaehlerpunktIDDoesNotCollideWithZaehlerpunkt(t *testing.T) {
	vars := pathVars{Zaehlerpunkt: "AT001", ZaehlerpunktID: 7}
	got := renderPath("<zaehlerpunkt_id>-<zaehlerpunkt>.csv", vars, time.Now())
	want := "7-AT001.csv"
	if got != want {
		t.Errorf("renderPath = %q, want %q", got, want)
	}
}

func TestGroupRowsByPath_YearOnlyTemplateSplitsPerYear(t *testing.T) {
	rows := []outputRow{
		{Timestamp: time.Date(2023, 12, 31, 23, 0, 0, 0, time.UTC), Value: 1},
		{Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Value: 2},
	}
	groups := groupRowsByPath("<yyyy>/data.csv", pathVars{}, rows)
	if len(groups) != 2 {
		t.Fatalf("groups = %+v, want 2 (one per year)", groups)
	}
	if len(groups["2023/data.csv"]) != 1 || len(groups["2024/data.csv"]) != 1 {
		t.Errorf("groups = %+v", groups)
	}
}

func TestGroupRowsByPath_NoDatePlaceholderSingleGroup(t *testing.T) {
	rows := []outputRow{
		{Timestamp: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Value: 1},
		{Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Value: 2},
	}
	groups := groupRowsByPath("data.csv", pathVars{}, rows)
	if len(groups) != 1 || len(groups["data.csv"]) != 2 {
		t.Errorf("groups = %+v, want single group with both rows", groups)
	}
}

func TestOutFormat(t *testing.T) {
	if f, err := outFormat("a/b/data.csv"); err != nil || f != "csv" {
		t.Errorf("outFormat(.csv) = %q, %v", f, err)
	}
	if f, err := outFormat("a/b/data.json"); err != nil || f != "json" {
		t.Errorf("outFormat(.json) = %q, %v", f, err)
	}
	if _, err := outFormat("a/b/data.txt"); err == nil {
		t.Error("outFormat(.txt) = nil error, want error")
	}
}

func TestWriteGroupedOutput_WritesOneFilePerYear(t *testing.T) {
	dir := t.TempDir()
	rows := toOutputRows([]provider.Reading{
		{Timestamp: time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC), Value: 10},
		{Timestamp: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), Value: 20},
	})
	tmpl := filepath.Join(dir, "<yyyy>", "data.csv")
	if err := writeGroupedOutput(tmpl, pathVars{}, rows); err != nil {
		t.Fatalf("writeGroupedOutput: %v", err)
	}
	for _, year := range []string{"2023", "2024"} {
		data, err := os.ReadFile(filepath.Join(dir, year, "data.csv"))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", year, err)
		}
		if !strings.Contains(string(data), "timestamp,value,unit,quality") {
			t.Errorf("%s/data.csv missing CSV header: %s", year, data)
		}
	}
}

func TestWriteGroupedOutput_JSONFormat(t *testing.T) {
	dir := t.TempDir()
	rows := toOutputRows([]provider.Reading{{Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Value: 5}})
	tmpl := filepath.Join(dir, "data.json")
	if err := writeGroupedOutput(tmpl, pathVars{}, rows); err != nil {
		t.Fatalf("writeGroupedOutput: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "data.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `"value": 5`) {
		t.Errorf("data.json = %s, want it to contain the reading", data)
	}
}
