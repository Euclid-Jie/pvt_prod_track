package main

import (
	"bytes"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestExportExcelIncludesRecentMonthBetweenWeekAndYtd(t *testing.T) {
	var output bytes.Buffer
	funds := []Fund{{RecentWeek: "1.00", RecentMonth: "2.00", Ytd: "3.00"}}
	if err := exportExcel(funds, &output); err != nil {
		t.Fatalf("exportExcel() error = %v", err)
	}

	workbook, err := excelize.OpenReader(bytes.NewReader(output.Bytes()))
	if err != nil {
		t.Fatalf("open exported workbook: %v", err)
	}
	defer workbook.Close()

	sheet := workbook.GetSheetName(0)
	for cell, want := range map[string]string{
		"E1": "近一周(%)", "F1": "近一月(%)", "G1": "今年以来(%)",
		"E2": "1.00", "F2": "2.00", "G2": "3.00",
	} {
		got, err := workbook.GetCellValue(sheet, cell)
		if err != nil {
			t.Fatalf("GetCellValue(%s): %v", cell, err)
		}
		if got != want {
			t.Errorf("cell %s = %q, want %q", cell, got, want)
		}
	}
}
