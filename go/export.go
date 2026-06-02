package main

import (
	"fmt"
	"io"
	"time"

	"github.com/xuri/excelize/v2"
)

func exportExcel(funds []Fund, w io.Writer) error {
	f := excelize.NewFile()
	sheet := "私募产品周报"
	f.SetSheetName("Sheet1", sheet)

	headers := []string{"策略类型", "管理人", "产品名称", "规模", "净值起始日",
		"近一周(%)", "今年以来(%)", "近一年(%)", "近一年夏普", "近一年最大回撤(%)",
		"2025(%)", "2024(%)", "2023(%)"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
	}

	for row, fund := range funds {
		vals := []string{fund.Strategy, fund.Manager, fund.ProductName, fund.Scale, fund.StartDate,
			fund.RecentWeek, fund.Ytd, fund.RecentYear, fund.RecentYearSharpe, fund.RecentYearMdd,
			fund.Y2025, fund.Y2024, fund.Y2023}
		for col, v := range vals {
			cell, _ := excelize.CoordinatesToCellName(col+1, row+2)
			f.SetCellValue(sheet, cell, v)
		}
	}

	_, err := f.WriteTo(w)
	return err
}

func excelFilename() string {
	return fmt.Sprintf("私募产品周报_%s.xlsx", time.Now().Format("20060102_150405"))
}
