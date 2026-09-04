package main

import (
	"bytes"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestExportExcelGroupsByStrategyAndOmitsProductName(t *testing.T) {
	var output bytes.Buffer
	funds := []Fund{
		{Strategy: "量化多头", Manager: "管理人A", ProductName: "不应导出", Scale: "50-100亿元", RecentWeek: "1.00", RecentMonth: "2.00", Ytd: "3.00", RecentYear: "4.00", Y2025: "5.00", Y2024: "6.00", Y2023: "7.00"},
		{Strategy: "CTA", Manager: "管理人B", RecentWeek: "5.00"},
	}
	if err := exportExcel(funds, &output); err != nil {
		t.Fatalf("exportExcel() error = %v", err)
	}

	workbook, err := excelize.OpenReader(bytes.NewReader(output.Bytes()))
	if err != nil {
		t.Fatalf("open exported workbook: %v", err)
	}
	defer workbook.Close()

	if got := workbook.GetSheetList(); len(got) != 2 || got[0] != "CTA" || got[1] != "量化多头" {
		t.Fatalf("sheet list = %v, want [CTA 量化多头]", got)
	}
	sheet := "量化多头"
	for cell, want := range map[string]string{
		"A1": "管理人", "B1": "规模", "C1": "策略类型", "D1": "近一周(%)", "E1": "近一月(%)", "F1": "今年以来(%)", "G1": "近一年(%)", "H1": "2025(%)", "I1": "2024(%)", "J1": "2023(%)",
		"A2": "管理人A", "B2": "50-100亿元", "C2": "量化多头", "D2": "1.00", "E2": "2.00", "F2": "3.00", "G2": "4.00", "H2": "5.00", "I2": "6.00", "J2": "7.00",
	} {
		got, err := workbook.GetCellValue(sheet, cell)
		if err != nil {
			t.Fatalf("GetCellValue(%s): %v", cell, err)
		}
		if got != want {
			t.Errorf("cell %s = %q, want %q", cell, got, want)
		}
	}
	if got, err := workbook.GetCellValue(sheet, "K1"); err != nil || got != "" {
		t.Fatalf("unexpected extra export column K1 = %q (err=%v)", got, err)
	}
}

func TestExportExcelAddsExcessSheetForEligibleStrategy(t *testing.T) {
	var output bytes.Buffer
	funds := []Fund{
		{
			Strategy: "1000增强", Manager: "管理人A", Scale: "50-100亿元", HasExcess: true,
			RecentWeek: "1.00", RecentMonth: "2.00", Ytd: "3.00", RecentYear: "4.00", Y2025: "5.00", Y2024: "6.00", Y2023: "7.00",
			ExcessRecentWeek: "0.10", ExcessRecentMonth: "0.20", ExcessYtd: "0.30", ExcessRecentYear: "0.40", ExcessY2025: "0.50", ExcessY2024: "0.60", ExcessY2023: "0.70",
		},
		{Strategy: "1000增强", Manager: "中证1000指数", RecentWeek: "8.00"},
	}
	if err := exportExcel(funds, &output); err != nil {
		t.Fatalf("exportExcel() error = %v", err)
	}

	workbook, err := excelize.OpenReader(bytes.NewReader(output.Bytes()))
	if err != nil {
		t.Fatalf("open exported workbook: %v", err)
	}
	defer workbook.Close()

	if got := workbook.GetSheetList(); len(got) != 2 || got[0] != "1000增强" || got[1] != "1000增强_超额" {
		t.Fatalf("sheet list = %v, want [1000增强 1000增强_超额]", got)
	}
	for cell, want := range map[string]string{
		"A1": "管理人", "A2": "管理人A", "D2": "1.00", "A3": "中证1000指数", "D3": "8.00",
	} {
		got, err := workbook.GetCellValue("1000增强", cell)
		if err != nil {
			t.Fatalf("GetCellValue(%s): %v", cell, err)
		}
		if got != want {
			t.Errorf("absolute sheet cell %s = %q, want %q", cell, got, want)
		}
	}
	for cell, want := range map[string]string{
		"A1": "管理人", "A2": "管理人A", "D2": "0.10", "E2": "0.20", "F2": "0.30", "G2": "0.40", "H2": "0.50", "I2": "0.60", "J2": "0.70",
	} {
		got, err := workbook.GetCellValue("1000增强_超额", cell)
		if err != nil {
			t.Fatalf("GetCellValue(%s): %v", cell, err)
		}
		if got != want {
			t.Errorf("excess sheet cell %s = %q, want %q", cell, got, want)
		}
	}
	if got, err := workbook.GetCellValue("1000增强_超额", "A3"); err != nil || got != "" {
		t.Fatalf("benchmark index should be omitted from excess sheet, A3 = %q (err=%v)", got, err)
	}
}
