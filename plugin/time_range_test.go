package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func absoluteQuery(start, end, zone, bucket string) map[string][]string {
	return map[string][]string{"start_at": {start}, "end_at": {end}, "timezone": {zone}, "bucket": {bucket}}
}

func queryTimeEvents(t *testing.T, q map[string][]string) ([]outEvent, []outBucket, string) {
	t.Helper()
	body, err := usageResponse(q)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Events   []outEvent
		Buckets  []outBucket
		Timezone string
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result.Events, result.Buckets, result.Timezone
}

func seedTimeEvents(t *testing.T, timestamps ...string) {
	t.Helper()
	for _, ts := range timestamps {
		body, err := json.Marshal(map[string]any{"Model": ts, "RequestedAt": ts, "Detail": map[string]int64{"InputTokens": 10, "OutputTokens": 2}})
		if err != nil {
			t.Fatal(err)
		}
		handleUsage(body)
	}
}

func TestAbsoluteDateRangeIndependentOfServerTimezone(t *testing.T) {
	for _, serverZone := range []string{"UTC", "America/Los_Angeles"} {
		t.Run(serverZone, func(t *testing.T) {
			previous := time.Local
			var err error
			time.Local, err = time.LoadLocation(serverZone)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { time.Local = previous })
			openTestDB(t, filepath.Join(t.TempDir(), "bounds.sqlite"))
			seedTimeEvents(t,
				"2026-09-09T23:59:59.999+08:00", "2026-09-10T00:00:00+08:00",
				"2026-09-10T12:00:00+08:00", "2026-09-10T23:59:59.999+08:00", "2026-09-11T00:00:00+08:00")
			q := absoluteQuery("2026-09-09T16:00:00Z", "2026-09-10T16:00:00Z", "Asia/Shanghai", "hour")
			events, bins, zone := queryTimeEvents(t, q)
			if len(events) != 3 || zone != "Asia/Shanghai" {
				t.Fatalf("events=%d timezone=%s", len(events), zone)
			}
			var labels []string
			var input, output int64
			for _, bin := range bins {
				labels = append(labels, bin.Label)
				input += bin.Input
				output += bin.Output
			}
			if !reflect.DeepEqual(labels, []string{"2026-09-10 00:00", "2026-09-10 12:00", "2026-09-10 23:00"}) || input != 30 || output != 6 {
				t.Fatalf("bins=%+v", bins)
			}
			for _, bucket := range []string{"day", "month"} {
				q["bucket"] = []string{bucket}
				_, bins, _ := queryTimeEvents(t, q)
				want := "2026-09-10"
				if bucket == "month" {
					want = "2026-09"
				}
				if len(bins) != 1 || bins[0].Label != want || bins[0].Requests != 3 {
					t.Fatalf("%s bins=%+v", bucket, bins)
				}
			}
			// The next day includes its midnight exactly once.
			next, _, _ := queryTimeEvents(t, absoluteQuery("2026-09-10T16:00:00Z", "2026-09-11T16:00:00Z", "Asia/Shanghai", "day"))
			if len(next) != 1 || next[0].Model != "2026-09-11T00:00:00+08:00" {
				t.Fatalf("next day events=%+v", next)
			}
		})
	}
}

func TestAbsoluteDateRangeAcrossDST(t *testing.T) {
	previous := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = previous })
	for _, tc := range []struct {
		name, start, end, day string
		hours                 int
	}{
		{"spring", "2026-03-08T05:00:00Z", "2026-03-09T04:00:00Z", "2026-03-08", 23},
		{"fall", "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z", "2026-11-01", 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			openTestDB(t, filepath.Join(t.TempDir(), "dst.sqlite"))
			start, _ := time.Parse(time.RFC3339, tc.start)
			for hour := -1; hour <= tc.hours; hour++ {
				seedTimeEvents(t, start.Add(time.Duration(hour)*time.Hour).Format(time.RFC3339))
			}
			events, bins, _ := queryTimeEvents(t, absoluteQuery(tc.start, tc.end, "America/New_York", "day"))
			if len(events) != tc.hours || len(bins) != 1 || bins[0].Label != tc.day || bins[0].Requests != int64(tc.hours) {
				t.Fatalf("events=%d bins=%+v", len(events), bins)
			}
		})
	}
}

func TestInvalidAbsoluteDateRangeReturnsBadRequest(t *testing.T) {
	valid := absoluteQuery("2026-09-09T16:00:00Z", "2026-09-10T16:00:00Z", "Asia/Shanghai", "day")
	for _, tc := range []struct{ key, value string }{
		{"start_at", ""}, {"end_at", ""}, {"timezone", ""},
		{"start_at", "2026-09-10"}, {"end_at", "2026-09-10T16:00:00"},
		{"end_at", "2026-09-09T16:00:00Z"}, {"end_at", "2026-09-08T16:00:00Z"},
		{"timezone", "Local"}, {"timezone", "Not/AZone"}, {"start", "2026-09-10"},
	} {
		q := make(map[string][]string)
		for key, value := range valid {
			q[key] = value
		}
		q[tc.key] = []string{tc.value}
		req, _ := json.Marshal(map[string]any{"Method": "GET", "Path": "/v0/management/plugins/cpa-usage-ma/api/usage", "Query": q})
		raw, err := handleManagement(req)
		if err != nil {
			t.Fatal(err)
		}
		var env envelope
		var response struct{ StatusCode int }
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(env.Result, &response); err != nil || response.StatusCode != 400 {
			t.Fatalf("%s=%q: response=%s err=%v", tc.key, tc.value, raw, err)
		}
	}
}

func TestLegacyDatesRetainServerLocalBoundaries(t *testing.T) {
	previous := time.Local
	var err error
	time.Local, err = time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { time.Local = previous })
	openTestDB(t, filepath.Join(t.TempDir(), "legacy.sqlite"))
	seedTimeEvents(t, "2026-09-09T23:59:59+08:00", "2026-09-10T00:00:00+08:00", "2026-09-11T00:00:00+08:00")
	events, bins, zone := queryTimeEvents(t, map[string][]string{"start": {"2026-09-10"}, "end": {"2026-09-10"}})
	if len(events) != 1 || len(bins) != 1 || bins[0].Label != "2026-09-10" || zone != "CST" {
		t.Fatalf("events=%+v bins=%+v timezone=%s", events, bins, zone)
	}
	window, err := parseUsageTimeRange(nil)
	if err != nil || window.end.Sub(window.start) != 7*24*time.Hour {
		t.Fatalf("legacy default range=%+v err=%v", window, err)
	}
}
