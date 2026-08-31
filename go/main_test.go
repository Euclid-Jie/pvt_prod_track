package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func preserveRuntimeState(t *testing.T) {
	t.Helper()
	oldCfg := cfg
	oldIntervals := intervals
	oldWeek := weekIV
	oldMonth := monthIV
	oldYTD := ytdIV
	oldHasConfig := hasConfig
	oldDataDir := dataDir
	dataCache.mu.Lock()
	oldFunds := dataCache.funds
	dataCache.mu.Unlock()
	t.Cleanup(func() {
		cfg = oldCfg
		intervals = oldIntervals
		weekIV = oldWeek
		monthIV = oldMonth
		ytdIV = oldYTD
		hasConfig = oldHasConfig
		dataDir = oldDataDir
		replaceCache(oldFunds)
	})
}

func writeTestConfig(t *testing.T, value Config) string {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUpdateLastDayValidatesDataBeforeSaving(t *testing.T) {
	preserveRuntimeState(t)
	dataDir = t.TempDir()
	cfg = Config{DBHost: "db.internal", DBPort: "3306", DBUser: "reader", DBPass: "secret", LastDay: "2026-08-21"}
	hasConfig = true
	configPath := writeTestConfig(t, cfg)

	result, err := updateLastDayWithLoader("2026-08-28", func(candidate []Interval) ([]Fund, error) {
		if len(candidate) == 0 || candidate[0].Name != "recent_week" || candidate[0].End != "2026-08-28" {
			t.Fatalf("candidate intervals = %+v", candidate)
		}
		return []Fund{{Manager: "验证产品"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.LastDay != "2026-08-28" || result.WeekEnd != "2026-08-28" || result.FundCount != 1 {
		t.Fatalf("result = %+v", result)
	}
	if cfg.LastDay != "2026-08-28" || weekIV.End != "2026-08-28" {
		t.Fatalf("runtime state = cfg:%+v week:%+v", cfg, weekIV)
	}

	var saved Config
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.LastDay != "2026-08-28" || saved.DBHost != "db.internal" || saved.DBUser != "reader" || saved.DBPass != "secret" {
		t.Fatalf("saved config = %+v", saved)
	}
}

func TestUpdateLastDayRejectsInvalidOrEmptyDataWithoutSaving(t *testing.T) {
	preserveRuntimeState(t)
	dataDir = t.TempDir()
	cfg = Config{DBHost: "db.internal", DBPort: "3306", DBUser: "reader", DBPass: "secret", LastDay: "2026-08-21"}
	hasConfig = true
	configPath := writeTestConfig(t, cfg)
	original, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	loaderCalled := false
	_, err = updateLastDayWithLoader("2026/08/28", func([]Interval) ([]Fund, error) {
		loaderCalled = true
		return nil, nil
	})
	if !errors.Is(err, errInvalidLastDay) || loaderCalled {
		t.Fatalf("invalid date error = %v, loaderCalled = %v", err, loaderCalled)
	}

	_, err = updateLastDayWithLoader("2026-08-28", func([]Interval) ([]Fund, error) {
		return []Fund{}, nil
	})
	if !errors.Is(err, errLastDayNoData) {
		t.Fatalf("empty data error = %v", err)
	}
	if cfg.LastDay != "2026-08-21" {
		t.Fatalf("last_day changed to %q", cfg.LastDay)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("config changed after rejected update\nbefore: %s\nafter: %s", original, after)
	}
}
