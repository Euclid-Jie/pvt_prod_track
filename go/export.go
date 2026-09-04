package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

func exportExcel(funds []Fund, w io.Writer) error {
	f := excelize.NewFile()

	// Keep the export aligned with the Web table's identity and return columns.
	// Product names and non-return risk columns are intentionally omitted.
	headers := []string{"管理人", "规模", "策略类型", "近一周(%)", "近一月(%)", "今年以来(%)", "近一年(%)", "2025(%)", "2024(%)", "2023(%)"}
	groups := make(map[string][]Fund)
	for _, fund := range funds {
		strategy := fund.Strategy
		if strategy == "" {
			strategy = "-"
		}
		groups[strategy] = append(groups[strategy], fund)
	}

	strategies := make([]string, 0, len(groups))
	for strategy := range groups {
		strategies = append(strategies, strategy)
	}
	sort.Strings(strategies)
	if len(strategies) == 0 {
		strategies = []string{"私募产品周报"}
		groups[strategies[0]] = nil
	}

	usedNames := make(map[string]struct{}, len(strategies))
	for index, strategy := range strategies {
		sheet := uniqueSheetName(strategy, usedNames)
		if index == 0 {
			if err := f.SetSheetName("Sheet1", sheet); err != nil {
				return err
			}
		} else if _, err := f.NewSheet(sheet); err != nil {
			return err
		}
		strategyFunds := groups[strategy]
		hasExcess := false
		for _, fund := range strategyFunds {
			if fund.HasExcess {
				hasExcess = true
				break
			}
		}
		if !hasExcess {
			if err := writeExportTable(f, sheet, 1, headers, strategyFunds, strategy, false); err != nil {
				return err
			}
			continue
		}

		if err := writeExportSectionTitle(f, sheet, 1, len(headers), "绝对收益"); err != nil {
			return err
		}
		if err := writeExportTable(f, sheet, 2, headers, strategyFunds, strategy, false); err != nil {
			return err
		}
		excessFunds := make([]Fund, 0, len(strategyFunds))
		for _, fund := range strategyFunds {
			if !strings.Contains(fund.Manager, "指数") {
				excessFunds = append(excessFunds, fund)
			}
		}
		excessTitleRow := len(strategyFunds) + 4
		if err := writeExportSectionTitle(f, sheet, excessTitleRow, len(headers), "超额收益"); err != nil {
			return err
		}
		if err := writeExportTable(f, sheet, excessTitleRow+1, headers, excessFunds, strategy, true); err != nil {
			return err
		}
	}

	_, err := f.WriteTo(w)
	return err
}

func writeExportSectionTitle(f *excelize.File, sheet string, row, columnCount int, title string) error {
	start, _ := excelize.CoordinatesToCellName(1, row)
	end, _ := excelize.CoordinatesToCellName(columnCount, row)
	if err := f.SetCellValue(sheet, start, title); err != nil {
		return err
	}
	return f.MergeCell(sheet, start, end)
}

func writeExportTable(f *excelize.File, sheet string, headerRow int, headers []string, funds []Fund, strategy string, excess bool) error {
	for col, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(col+1, headerRow)
		if err := f.SetCellValue(sheet, cell, header); err != nil {
			return err
		}
	}
	for row, fund := range funds {
		values := []string{fund.Manager, fund.Scale, strategy,
			fund.RecentWeek, fund.RecentMonth, fund.Ytd, fund.RecentYear, fund.Y2025, fund.Y2024, fund.Y2023}
		if excess {
			values = []string{fund.Manager, fund.Scale, strategy,
				fund.ExcessRecentWeek, fund.ExcessRecentMonth, fund.ExcessYtd, fund.ExcessRecentYear,
				fund.ExcessY2025, fund.ExcessY2024, fund.ExcessY2023}
		}
		for col, value := range values {
			cell, _ := excelize.CoordinatesToCellName(col+1, headerRow+row+1)
			if err := f.SetCellValue(sheet, cell, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func uniqueSheetName(strategy string, used map[string]struct{}) string {
	name := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', '*', '?', ':', '[', ']':
			return '-'
		default:
			return r
		}
	}, strings.TrimSpace(strategy))
	if name == "" {
		name = "-"
	}
	runes := []rune(name)
	if len(runes) > 31 {
		name = string(runes[:31])
	}
	base := name
	for suffix := 2; ; suffix++ {
		if _, exists := used[name]; !exists {
			used[name] = struct{}{}
			return name
		}
		tail := fmt.Sprintf("_%d", suffix)
		limit := 31 - len([]rune(tail))
		baseRunes := []rune(base)
		if len(baseRunes) > limit {
			baseRunes = baseRunes[:limit]
		}
		name = string(baseRunes) + tail
	}
}

func excelFilename() string {
	return fmt.Sprintf("私募产品周报_%s.xlsx", time.Now().Format("20060102_150405"))
}
