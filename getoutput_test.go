// getoutput_test.go
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
)

func TestToOutputRows_AddsUnit(t *testing.T) {
	in := []provider.Reading{{Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), Value: 42, Quality: "L2"}}
	out := toOutputRows(in)
	if len(out) != 1 {
		t.Fatalf("toOutputRows = %+v, want 1 row", out)
	}
	if out[0].Unit != provider.Unit || out[0].Value != 42 || out[0].Quality != "L2" {
		t.Errorf("toOutputRows[0] = %+v", out[0])
	}
}

func TestWriteText_SinglePointNoHeader(t *testing.T) {
	points := []pointOutput{{Point: "AT001", Readings: []outputRow{
		{Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), Value: 42, Unit: "Wh"},
	}}}
	var buf bytes.Buffer
	if err := writeText(&buf, points); err != nil {
		t.Fatalf("writeText: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "AT001") {
		t.Errorf("single-point text output should not print a point header, got: %s", out)
	}
	if !strings.Contains(out, "42") || !strings.Contains(out, "Wh") {
		t.Errorf("text output missing value/unit: %s", out)
	}
}

func TestWriteText_MultiPointHasHeaders(t *testing.T) {
	points := []pointOutput{
		{Point: "AT001", PointName: "Consumption", Readings: []outputRow{{Timestamp: time.Now(), Value: 1, Unit: "Wh"}}},
		{Point: "AT002", PointName: "Production", Readings: []outputRow{{Timestamp: time.Now(), Value: 2, Unit: "Wh"}}},
	}
	var buf bytes.Buffer
	if err := writeText(&buf, points); err != nil {
		t.Fatalf("writeText: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "AT001") || !strings.Contains(out, "AT002") {
		t.Errorf("multi-point text output missing point headers: %s", out)
	}
}

func TestWriteJSON_SinglePointIsBareReadingsArray(t *testing.T) {
	points := []pointOutput{{Point: "AT001", Readings: []outputRow{
		{Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), Value: 42, Unit: "Wh"},
	}}}
	var buf bytes.Buffer
	if err := writeJSON(&buf, points); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	var got []outputRow
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decoding single-point JSON as bare array: %v (got: %s)", err, buf.String())
	}
	if len(got) != 1 || got[0].Value != 42 {
		t.Errorf("decoded = %+v", got)
	}
}

func TestWriteJSON_MultiPointIsWrappedArray(t *testing.T) {
	points := []pointOutput{
		{Point: "AT001", Provider: "evn", Sample: "raw", Readings: []outputRow{{Value: 1, Unit: "Wh"}}},
		{Point: "AT002", Provider: "evn", Sample: "raw", Readings: []outputRow{{Value: 2, Unit: "Wh"}}},
	}
	var buf bytes.Buffer
	if err := writeJSON(&buf, points); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	var got []pointOutput
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decoding multi-point JSON as wrapped array: %v (got: %s)", err, buf.String())
	}
	if len(got) != 2 || got[0].Point != "AT001" || got[1].Point != "AT002" {
		t.Errorf("decoded = %+v", got)
	}
}

func TestWriteCSV_HeaderAndRows(t *testing.T) {
	rows := []outputRow{
		{Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), Value: 42, Unit: "Wh", Quality: "L2"},
	}
	var buf bytes.Buffer
	if err := writeCSV(&buf, rows); err != nil {
		t.Fatalf("writeCSV: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("csv output = %q, want header + 1 data row", buf.String())
	}
	if lines[0] != "timestamp,value,unit,quality" {
		t.Errorf("header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "42") || !strings.Contains(lines[1], "Wh") || !strings.Contains(lines[1], "L2") {
		t.Errorf("data row = %q", lines[1])
	}
}
