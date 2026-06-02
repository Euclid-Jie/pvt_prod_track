package main

import (
	"bufio"
	"bytes"
	"os"
	"sort"
	"strings"
	"time"
)

func loadHolidays(path string) (map[time.Time]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return loadHolidaysFromBytes(data)
}

func loadHolidaysFromBytes(data []byte) (map[time.Time]bool, error) {
	holidays := make(map[time.Time]bool)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		t, err := time.Parse("2006-01-02", line)
		if err == nil {
			holidays[t] = true
		}
	}
	return holidays, nil
}

// Returns sorted trading days and friday trading dates in [begin, end].
func generateTradingDates(begin, end time.Time, holidays map[time.Time]bool) ([]time.Time, []time.Time) {
	var trading []time.Time
	for d := begin; !d.After(end); d = d.AddDate(0, 0, 1) {
		wd := d.Weekday()
		if wd == time.Saturday || wd == time.Sunday {
			continue
		}
		if !holidays[d] {
			trading = append(trading, d)
		}
	}

	// for each Friday in range, find the last trading day <= that Friday
	tradingSet := make(map[time.Time]bool, len(trading))
	for _, t := range trading {
		tradingSet[t] = true
	}

	fridayMap := make(map[time.Time]bool)
	for d := begin; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() != time.Friday {
			continue
		}
		// find last trading day <= d
		candidate := d
		for i := 0; i < 7; i++ {
			if tradingSet[candidate] {
				fridayMap[candidate] = true
				break
			}
			candidate = candidate.AddDate(0, 0, -1)
		}
	}

	var fridays []time.Time
	for t := range fridayMap {
		fridays = append(fridays, t)
	}
	sort.Slice(fridays, func(i, j int) bool { return fridays[i].Before(fridays[j]) })
	return trading, fridays
}

type Interval struct {
	Name  string `json:"name"`
	Begin string `json:"begin"`
	End   string `json:"end"`
}

func buildIntervals(lastDay time.Time, yearly []Interval, holidays map[time.Time]bool) []Interval {
	begin := lastDay.AddDate(0, 0, -380)
	end := lastDay.AddDate(0, 0, 10)
	_, fridays := generateTradingDates(begin, end, holidays)

	// fridays before lastDay
	var beforeLast []time.Time
	for _, f := range fridays {
		if f.Before(lastDay) {
			beforeLast = append(beforeLast, f)
		}
	}

	endStr := lastDay.Format("2006-01-02")
	yearStart := time.Date(lastDay.Year(), 1, 1, 0, 0, 0, 0, time.UTC)

	findFirst := func(after time.Time) time.Time {
		for _, f := range fridays {
			if !f.Before(after) {
				return f
			}
		}
		return fridays[len(fridays)-1]
	}

	recentWeekBegin := beforeLast[len(beforeLast)-1]
	recentMonthBegin := findFirst(lastDay.AddDate(0, 0, -30))
	ytdBegin := beforeLast[len(beforeLast)-1] // will be overridden below
	// last friday before year start
	for i := len(beforeLast) - 1; i >= 0; i-- {
		if beforeLast[i].Before(yearStart) {
			ytdBegin = beforeLast[i]
			break
		}
	}
	recentYearBegin := findFirst(lastDay.AddDate(0, 0, -365))

	dynamic := []Interval{
		{Name: "recent_week", Begin: recentWeekBegin.Format("2006-01-02"), End: endStr},
		{Name: "recent_month", Begin: recentMonthBegin.Format("2006-01-02"), End: endStr},
		{Name: "ytd", Begin: ytdBegin.Format("2006-01-02"), End: endStr},
		{Name: "recent_year", Begin: recentYearBegin.Format("2006-01-02"), End: endStr},
	}
	return append(dynamic, yearly...)
}
