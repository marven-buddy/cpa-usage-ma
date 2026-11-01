package main

import (
	"fmt"
	"time"
	_ "time/tzdata"
)

type usageTimeRange struct {
	start, end time.Time
	location   *time.Location
	timezone   string
}

func parseUsageTimeRange(q map[string][]string) (usageTimeRange, error) {
	get := func(key string) string {
		if values := q[key]; len(values) > 0 {
			return values[0]
		}
		return ""
	}
	_, hasStart := q["start_at"]
	_, hasEnd := q["end_at"]
	_, hasZone := q["timezone"]
	if hasStart || hasEnd || hasZone {
		// Absolute bounds are a complete contract; never silently fall back to
		// server-local dates when one of the new parameters is missing or invalid.
		if get("start") != "" || get("end") != "" {
			return usageTimeRange{}, fmt.Errorf("不能混用日期参数和绝对时间范围")
		}
		start, err := time.Parse(time.RFC3339Nano, get("start_at"))
		if err != nil {
			return usageTimeRange{}, fmt.Errorf("无效开始时间，需包含时区")
		}
		end, err := time.Parse(time.RFC3339Nano, get("end_at"))
		if err != nil {
			return usageTimeRange{}, fmt.Errorf("无效结束时间，需包含时区")
		}
		if !start.Before(end) {
			return usageTimeRange{}, fmt.Errorf("结束时间必须晚于开始时间")
		}
		zone := get("timezone")
		if zone == "" || zone == "Local" {
			return usageTimeRange{}, fmt.Errorf("请提供有效的统计时区")
		}
		location, err := time.LoadLocation(zone)
		if err != nil {
			return usageTimeRange{}, fmt.Errorf("无效统计时区")
		}
		return usageTimeRange{start, end, location, zone}, nil
	}

	// Legacy YYYY-MM-DD callers retain their server-local, inclusive date range.
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	start, end := today.AddDate(0, 0, -6), today
	if value := get("start"); value != "" {
		var err error
		start, err = time.ParseInLocation("2006-01-02", value, time.Local)
		if err != nil {
			return usageTimeRange{}, fmt.Errorf("无效开始日期")
		}
	}
	if value := get("end"); value != "" {
		var err error
		end, err = time.ParseInLocation("2006-01-02", value, time.Local)
		if err != nil {
			return usageTimeRange{}, fmt.Errorf("无效结束日期")
		}
	}
	if start.After(end) {
		return usageTimeRange{}, fmt.Errorf("开始日期不能晚于结束日期")
	}
	return usageTimeRange{start, end.AddDate(0, 0, 1), time.Local, now.Format("MST")}, nil
}
