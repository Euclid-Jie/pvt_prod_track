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
	Strategy                 string   `json:"strategy"`
	Manager                  string   `json:"manager"`
	ProductName              string   `json:"product_name"`
	Scale                    string   `json:"scale"`
	ScaleLevel               string   `json:"scale_level"`
	RecentWeek               string   `json:"recent_week"`
	RecentWeekPrecise        *float64 `json:"recent_week_precise,omitempty"`
	RecentMonth              string   `json:"recent_month"`
	RecentMonthPrecise       *float64 `json:"recent_month_precise,omitempty"`
	Ytd                      string   `json:"ytd"`
	YtdPrecise               *float64 `json:"ytd_precise,omitempty"`
	RecentYear               string   `json:"recent_year"`
	RecentYearPrecise        *float64 `json:"recent_year_precise,omitempty"`
	RecentYearSharpe         string   `json:"recent_year_sharpe"`
	RecentYearMdd            string   `json:"recent_year_mdd"`
	Y2025                    string   `json:"y2025"`
	Y2024                    string   `json:"y2024"`
	Y2023                    string   `json:"y2023"`
	HasExcess                bool     `json:"has_excess"`
	ExcessRecentWeek         string   `json:"excess_recent_week"`
	ExcessRecentWeekPrecise  *float64 `json:"excess_recent_week_precise,omitempty"`
	ExcessRecentMonth        string   `json:"excess_recent_month"`
	ExcessRecentMonthPrecise *float64 `json:"excess_recent_month_precise,omitempty"`
	ExcessYtd                string   `json:"excess_ytd"`
	ExcessYtdPrecise         *float64 `json:"excess_ytd_precise,omitempty"`
	ExcessRecentYear         string   `json:"excess_recent_year"`
	ExcessRecentYearPrecise  *float64 `json:"excess_recent_year_precise,omitempty"`
	ExcessRecentYearSharpe   string   `json:"excess_recent_year_sharpe"`
	ExcessRecentYearMdd      string   `json:"excess_recent_year_mdd"`
	ExcessY2025              string   `json:"excess_y2025"`
	ExcessY2024              string   `json:"excess_y2024"`
	ExcessY2023              string   `json:"excess_y2023"`
}

type fundInfo struct {
	ProdCode   string
	ProdName   string
	ProdComp   string
	ProdType   string
	Scale      string
	CompCode   string
	NavSource  string
	Fid        sql.NullInt64
	MetricCode string
}

func (info fundInfo) metricCode() string {
	if info.MetricCode != "" {
		return info.MetricCode
	}
	if info.NavSource == "个人净值" && info.Fid.Valid {
		return fmt.Sprintf("p_%d", info.Fid.Int64)
	}
	return info.ProdCode
}

func appendFundInfoRows(infos []fundInfo, rows *sql.Rows) ([]fundInfo, error) {
	defer rows.Close()
	for rows.Next() {
		var r fundInfo
		if err := rows.Scan(&r.MetricCode, &r.ProdCode, &r.ProdName, &r.ProdComp, &r.ProdType, &r.Scale, &r.CompCode, &r.NavSource, &r.Fid); err != nil {
			return nil, err
		}
		infos = append(infos, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return infos, nil
}

func loadManagerScales(euclidDB *sql.DB) (map[string]string, error) {
	rows, err := euclidDB.Query("SELECT COALESCE(`登记编号`, ''), COALESCE(`管理规模`, '') FROM `量化私募管理人列表`")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	scales := make(map[string]string)
	for rows.Next() {
		var compCode, scale string
		if err := rows.Scan(&compCode, &scale); err != nil {
			return nil, err
		}
		if compCode != "" {
			scales[compCode] = scale
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return scales, nil
}

func loadBackupFundCodes(navDB *sql.DB) (map[string]struct{}, error) {
	rows, err := navDB.Query("SELECT fund_code FROM nav_product_preferences WHERE is_backup = 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	codes := make(map[string]struct{})
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		codes[code] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return codes, nil
}

func excludeBackupFundInfos(infos []fundInfo, backupCodes map[string]struct{}) []fundInfo {
	filtered := make([]fundInfo, 0, len(infos))
	for _, info := range infos {
		if _, isBackup := backupCodes[info.metricCode()]; isBackup {
			continue
		}
		filtered = append(filtered, info)
	}
	return filtered
}

func loadFundInfos(euclidDB, navDB *sql.DB) ([]fundInfo, error) {
	var infos []fundInfo
	managerScales, err := loadManagerScales(euclidDB)
	if err != nil {
		return nil, err
	}
	queries := []struct {
		db  *sql.DB
		sql string
	}{
		{euclidDB, "SELECT '', COALESCE(prod_code, ''), COALESCE(prod_name, ''), COALESCE(prod_comp, ''), COALESCE(prod_type, ''), COALESCE(管理人规模, ''), '', 净值来源, fid FROM fund_basic_info WHERE 净值来源 IS NOT NULL"},
		{navDB, "SELECT CONCAT('pending:', PROD_CODE), COALESCE(PROD_CODE, ''), COALESCE(PROD_NAME, ''), prod_comp, COALESCE(ProdType, ''), '', COALESCE(comp_code, ''), '', NULL FROM PendingFund WHERE prod_comp IS NOT NULL AND TRIM(prod_comp) <> ''"},
		{navDB, "SELECT CONCAT('alpha_plus:', a.PROD_CODE), COALESCE(a.PROD_CODE, ''), COALESCE(a.PROD_NAME, ''), COALESCE(a.prod_comp, ''), COALESCE(a.STRATEGY_TYPE, ''), COALESCE(s.管理规模, ''), COALESCE(a.comp_code, ''), '', NULL FROM AlphaPlusFund a LEFT JOIN Euclid.company_scale s ON a.prod_comp = s.prod_comp WHERE a.PROD_CODE IS NOT NULL AND TRIM(a.PROD_CODE) <> ''"},
		{navDB, "SELECT CONCAT('fof99:', register_number), COALESCE(register_number, ''), COALESCE(prod_name, ''), COALESCE(prod_comp, ''), COALESCE(prod_type, ''), '', COALESCE(comp_code, ''), '', NULL FROM fof99_nav_index WHERE prod_comp IS NOT NULL AND TRIM(prod_comp) <> '' AND register_number IS NOT NULL AND TRIM(register_number) <> ''"},
		{navDB, "SELECT CONCAT('smw:', register_number), COALESCE(register_number, ''), COALESCE(prod_name, ''), COALESCE(prod_comp, ''), COALESCE(prod_type, ''), '', COALESCE(comp_code, ''), '', NULL FROM smw_index WHERE prod_comp IS NOT NULL AND TRIM(prod_comp) <> '' AND register_number IS NOT NULL AND TRIM(register_number) <> ''"},
		{navDB, "SELECT CONCAT('mail:', product_key), COALESCE(product_key, ''), COALESCE(prod_name, ''), COALESCE(prod_comp, ''), COALESCE(prod_type, ''), '', COALESCE(comp_code, ''), '', NULL FROM mail_nav_index WHERE enabled = 1 AND prod_comp IS NOT NULL AND TRIM(prod_comp) <> '' AND product_key IS NOT NULL AND TRIM(product_key) <> '' AND prod_type IS NOT NULL AND TRIM(prod_type) <> ''"},
	}
	for _, q := range queries {
		rows, err := q.db.Query(q.sql)
		if err != nil {
			return nil, err
		}
		infos, err = appendFundInfoRows(infos, rows)
		if err != nil {
			return nil, err
		}
	}
	for i := range infos {
		if infos[i].Scale == "" && infos[i].CompCode != "" {
			infos[i].Scale = managerScales[infos[i].CompCode]
		}
	}
	backupCodes, err := loadBackupFundCodes(navDB)
	if err != nil {
		return nil, err
	}
	return excludeBackupFundInfos(infos, backupCodes), nil
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

func precisePctVal(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) {
		return nil
	}
	value := *v * 100
	return &value
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
	pivotSQL := "SELECT fund_code, is_excess, " + strings.Join(selectExprs, ", ") +
		" FROM nav_interval_metrics" +
		" WHERE is_excess IN (0,1) AND interval_end IN (" + strings.Join(ends, ",") + ")" +
		" GROUP BY fund_code, is_excess" +
		" HAVING `" + weekCol + "` IS NOT NULL"

	type pivotRow struct {
		fundCode string
		isExcess bool
		vals     []*float64
	}

	var wg sync.WaitGroup
	var pivotRows []pivotRow
	var infos []fundInfo
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
		dest := make([]any, 2+nCols)
		for rows.Next() {
			var code string
			var isExcess bool
			vals := make([]*float64, nCols)
			dest[0] = &code
			dest[1] = &isExcess
			for i := range vals {
				dest[i+2] = &vals[i]
			}
			if e := rows.Scan(dest...); e == nil {
				pivotRows = append(pivotRows, pivotRow{code, isExcess, vals})
			}
		}
		if err := rows.Err(); err != nil {
			errMetrics = err
		}
	}()
	go func() {
		defer wg.Done()
		infos, errInfo = loadFundInfos(euclidDB, navDB)
	}()
	wg.Wait()
	if errMetrics != nil {
		return nil, errMetrics
	}
	if errInfo != nil {
		return nil, errInfo
	}

	absoluteMap := make(map[string][]*float64, len(pivotRows))
	excessMap := make(map[string][]*float64, len(pivotRows))
	for _, pr := range pivotRows {
		if pr.isExcess {
			excessMap[pr.fundCode] = pr.vals
		} else {
			absoluteMap[pr.fundCode] = pr.vals
		}
	}

	get := func(metrics map[string][]*float64, code, colName string) *float64 {
		vals, ok := metrics[code]
		if !ok {
			return nil
		}
		i, ok := colIdx[colName]
		if !ok {
			return nil
		}
		return vals[i]
	}

	funds := make([]Fund, 0, len(infos))
	for _, info := range infos {
		code := info.metricCode()
		if info.ProdComp != "基准" && absoluteMap[code] == nil {
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
		absoluteRecentWeek := get(absoluteMap, code, "recent_week_return")
		absoluteRecentMonth := get(absoluteMap, code, "recent_month_return")
		absoluteYtd := get(absoluteMap, code, "ytd_return")
		absoluteRecentYear := get(absoluteMap, code, "recent_year_return")
		excessRecentWeek := get(excessMap, code, "recent_week_return")
		excessRecentMonth := get(excessMap, code, "recent_month_return")
		excessYtd := get(excessMap, code, "ytd_return")
		excessRecentYear := get(excessMap, code, "recent_year_return")
		funds = append(funds, Fund{
			Strategy:                 strategy,
			Manager:                  orDash(info.ProdComp),
			ProductName:              orDash(info.ProdName),
			Scale:                    scale,
			ScaleLevel:               scaleLevel,
			RecentWeek:               fmtVal(absoluteRecentWeek, true),
			RecentWeekPrecise:        precisePctVal(absoluteRecentWeek),
			RecentMonth:              fmtVal(absoluteRecentMonth, true),
			RecentMonthPrecise:       precisePctVal(absoluteRecentMonth),
			Ytd:                      fmtVal(absoluteYtd, true),
			YtdPrecise:               precisePctVal(absoluteYtd),
			RecentYear:               fmtVal(absoluteRecentYear, true),
			RecentYearPrecise:        precisePctVal(absoluteRecentYear),
			RecentYearSharpe:         fmtVal(get(absoluteMap, code, "recent_year_sharpe"), false),
			RecentYearMdd:            fmtVal(get(absoluteMap, code, "recent_year_MDD"), true),
			Y2025:                    fmtVal(get(absoluteMap, code, "y2025_return"), true),
			Y2024:                    fmtVal(get(absoluteMap, code, "y2024_return"), true),
			Y2023:                    fmtVal(get(absoluteMap, code, "y2023_return"), true),
			HasExcess:                excessMap[code] != nil,
			ExcessRecentWeek:         fmtVal(excessRecentWeek, true),
			ExcessRecentWeekPrecise:  precisePctVal(excessRecentWeek),
			ExcessRecentMonth:        fmtVal(excessRecentMonth, true),
			ExcessRecentMonthPrecise: precisePctVal(excessRecentMonth),
			ExcessYtd:                fmtVal(excessYtd, true),
			ExcessYtdPrecise:         precisePctVal(excessYtd),
			ExcessRecentYear:         fmtVal(excessRecentYear, true),
			ExcessRecentYearPrecise:  precisePctVal(excessRecentYear),
			ExcessRecentYearSharpe:   fmtVal(get(excessMap, code, "recent_year_sharpe"), false),
			ExcessRecentYearMdd:      fmtVal(get(excessMap, code, "recent_year_MDD"), true),
			ExcessY2025:              fmtVal(get(excessMap, code, "y2025_return"), true),
			ExcessY2024:              fmtVal(get(excessMap, code, "y2024_return"), true),
			ExcessY2023:              fmtVal(get(excessMap, code, "y2023_return"), true),
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
