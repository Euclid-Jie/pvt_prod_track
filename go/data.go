package main

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"sync"

	_ "github.com/go-sql-driver/mysql"
)

var strategyType = map[string]string{
	"CTA中短": "CTA", "CTA中长": "CTA", "CTA横截面": "CTA", "CTA基本面": "CTA",
	"CTA日内": "CTA", "CTA混合": "CTA", "CTA高频": "CTA", "CTA截面": "CTA",
	"高频CTA": "CTA", "主观期货": "CTA", "股指CTA": "CTA",
	"套利商品": "套利", "商品套利": "套利", "套利可转债": "套利", "套利股指": "套利",
	"股指套利": "套利", "套利ETF": "套利", "期权复合套利": "套利", "期权方向交易": "套利",
	"复合套利": "套利", "套利复合": "套利", "期货套利": "套利", "T0": "套利",
	"固收复合": "固收", "债券高收益债": "固收", "债券固收+": "固收", "债券纯债": "固收",
	"2000增强": "2000小微增强", "2000小微": "2000小微增强", "小市值微盘增强": "2000小微增强",
	"另类多头": "量选另类", "灵活对冲": "量选另类",
	"量选多头": "量化多头",
}

type Fund struct {
	Strategy         string `json:"strategy"`
	Manager          string `json:"manager"`
	ProductName      string `json:"product_name"`
	Scale            string `json:"scale"`
	ScaleLevel       string `json:"scale_level"`
	RecentWeek       string `json:"recent_week"`
	Ytd              string `json:"ytd"`
	RecentYear       string `json:"recent_year"`
	RecentYearSharpe string `json:"recent_year_sharpe"`
	RecentYearMdd    string `json:"recent_year_mdd"`
	Y2025            string `json:"y2025"`
	Y2024            string `json:"y2024"`
	Y2023            string `json:"y2023"`
}

type cache struct {
	mu    sync.Mutex
	funds []Fund
}

var dataCache cache

var (
	dbNav    *sql.DB
	dbEuclid *sql.DB
	dbMu     sync.Mutex
)

func initDBPools(cfg *Config) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	dsn := func(db string) string {
		return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=true&compress=true",
			cfg.DBUser, cfg.DBPass, cfg.DBHost, cfg.DBPort, db)
	}
	var err error
	if dbNav != nil {
		dbNav.Close()
	}
	if dbEuclid != nil {
		dbEuclid.Close()
	}
	dbNav, err = sql.Open("mysql", dsn("Nav"))
	if err != nil {
		return err
	}
	dbEuclid, err = sql.Open("mysql", dsn("Euclid"))
	return err
}

func fmtVal(v *float64, pct bool) string {
	if v == nil || math.IsNaN(*v) {
		return "-"
	}
	if pct {
		return fmt.Sprintf("%.2f", *v*100)
	}
	return fmt.Sprintf("%.4f", *v)
}

func pivotCol(begin, end, metric string) string {
	return fmt.Sprintf(
		"MAX(CASE WHEN interval_begin='%s' AND interval_end='%s' AND metric_name='%s' THEN metric_value END)",
		begin, end, metric)
}

func loadData(cfg *Config, intervals []Interval) ([]Fund, error) {
	dataCache.mu.Lock()
	defer dataCache.mu.Unlock()
	if dataCache.funds != nil {
		return dataCache.funds, nil
	}

	dbMu.Lock()
	navDB := dbNav
	euclidDB := dbEuclid
	dbMu.Unlock()
	if navDB == nil || euclidDB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	// Build pivot columns: return for every interval, sharpe+MDD only for recent_year.
	// To add a metric: append to colNames/selectCols and add a get() call in the Fund{} block below.
	type colDef struct{ name, colExpr string }
	var colDefs []colDef
	endSet := make(map[string]bool)
	for _, iv := range intervals {
		endSet[iv.End] = true
		colDefs = append(colDefs, colDef{iv.Name + "_return",
			pivotCol(iv.Begin, iv.End, "return") + " AS `" + iv.Name + "_return`"})
		if iv.Name == "recent_year" {
			colDefs = append(colDefs, colDef{"recent_year_sharpe",
				pivotCol(iv.Begin, iv.End, "sharpe") + " AS `recent_year_sharpe`"})
			colDefs = append(colDefs, colDef{"recent_year_MDD",
				pivotCol(iv.Begin, iv.End, "MDD") + " AS `recent_year_MDD`"})
		}
	}

	selectExprs := make([]string, len(colDefs))
	colIdx := make(map[string]int, len(colDefs))
	for i, c := range colDefs {
		selectExprs[i] = c.colExpr
		colIdx[c.name] = i
	}
	ends := make([]string, 0, len(endSet))
	for e := range endSet {
		ends = append(ends, "'"+e+"'")
	}
	// HAVING filters to funds that have recent_week data (the first col is always recent_week_return).
	weekCol := colDefs[0].name
	pivotSQL := "SELECT fund_code, " + strings.Join(selectExprs, ", ") +
		" FROM nav_interval_metrics" +
		" WHERE is_excess=0 AND interval_end IN (" + strings.Join(ends, ",") + ")" +
		" GROUP BY fund_code" +
		" HAVING `" + weekCol + "` IS NOT NULL"

	type pivotRow struct {
		fundCode string
		vals     []*float64
	}
	type infoRow struct {
		ProdCode string
		ProdName string
		ProdComp string
		ProdType string
		Scale    string
	}

	var wg sync.WaitGroup
	var pivotRows []pivotRow
	var infos []infoRow
	var errMetrics, errInfo error

	wg.Add(2)
	go func() {
		defer wg.Done()
		rows, err := navDB.Query(pivotSQL)
		if err != nil {
			errMetrics = err
			return
		}
		defer rows.Close()
		nCols := len(colDefs)
		// dest is reused across rows; vals is allocated per row to avoid aliasing.
		dest := make([]any, 1+nCols)
		for rows.Next() {
			var code string
			vals := make([]*float64, nCols)
			dest[0] = &code
			for i := range vals {
				dest[i+1] = &vals[i]
			}
			if e := rows.Scan(dest...); e == nil {
				pivotRows = append(pivotRows, pivotRow{code, vals})
			}
		}
	}()
	go func() {
		defer wg.Done()
		rows, err := euclidDB.Query(
			"SELECT prod_code, prod_name, prod_comp, prod_type, 管理人规模 FROM fund_basic_info WHERE 净值来源 IS NOT NULL")
		if err != nil {
			errInfo = err
			return
		}
		defer rows.Close()
		for rows.Next() {
			var r infoRow
			if e := rows.Scan(&r.ProdCode, &r.ProdName, &r.ProdComp, &r.ProdType, &r.Scale); e == nil {
				infos = append(infos, r)
			}
		}
	}()
	wg.Wait()
	if errMetrics != nil {
		return nil, errMetrics
	}
	if errInfo != nil {
		return nil, errInfo
	}

	pivotMap := make(map[string][]*float64, len(pivotRows))
	for _, pr := range pivotRows {
		pivotMap[pr.fundCode] = pr.vals
	}

	get := func(code, colName string) *float64 {
		vals, ok := pivotMap[code]
		if !ok {
			return nil
		}
		i, ok := colIdx[colName]
		if !ok {
			return nil
		}
		return vals[i]
	}

	var funds []Fund
	for _, info := range infos {
		code := info.ProdCode
		if info.ProdComp != "基准" && pivotMap[code] == nil {
			continue
		}
		scale := info.Scale
		if scale == "" {
			scale = "-"
		}
		scaleLevel := "小厂"
		if scale == "50-100亿元" || scale == "100亿元以上" {
			scaleLevel = "大厂"
		}
		strategy := info.ProdType
		if mapped, ok := strategyType[strategy]; ok {
			strategy = mapped
		}
		if strategy == "" {
			strategy = "-"
		}
		funds = append(funds, Fund{
			Strategy:         strategy,
			Manager:          orDash(info.ProdComp),
			ProductName:      orDash(info.ProdName),
			Scale:            scale,
			ScaleLevel:       scaleLevel,
			RecentWeek:       fmtVal(get(code, "recent_week_return"), true),
			Ytd:              fmtVal(get(code, "ytd_return"), true),
			RecentYear:       fmtVal(get(code, "recent_year_return"), true),
			RecentYearSharpe: fmtVal(get(code, "recent_year_sharpe"), false),
			RecentYearMdd:    fmtVal(get(code, "recent_year_MDD"), true),
			Y2025:            fmtVal(get(code, "y2025_return"), true),
			Y2024:            fmtVal(get(code, "y2024_return"), true),
			Y2023:            fmtVal(get(code, "y2023_return"), true),
		})
	}

	dataCache.funds = funds
	return funds, nil
}

func clearCache() {
	dataCache.mu.Lock()
	dataCache.funds = nil
	dataCache.mu.Unlock()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
