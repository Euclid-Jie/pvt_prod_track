package main

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

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
	StartDate        string `json:"start_date"`
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

func fmtVal(v *float64, pct bool) string {
	if v == nil || math.IsNaN(*v) {
		return "-"
	}
	if pct {
		return fmt.Sprintf("%.2f", *v*100)
	}
	return fmt.Sprintf("%.4f", *v)
}

func loadData(cfg *Config, intervals []Interval) ([]Fund, error) {
	dataCache.mu.Lock()
	defer dataCache.mu.Unlock()
	if dataCache.funds != nil {
		return dataCache.funds, nil
	}

	dsn := func(db string) string {
		return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=true",
			cfg.DBUser, cfg.DBPass, cfg.DBHost, cfg.DBPort, db)
	}
	navDB, err := sql.Open("mysql", dsn("Nav"))
	if err != nil {
		return nil, err
	}
	defer navDB.Close()
	euclidDB, err := sql.Open("mysql", dsn("Euclid"))
	if err != nil {
		return nil, err
	}
	defer euclidDB.Close()

	// build interval map (begin,end) -> name
	intervalMap := make(map[[2]string]string)
	endSet := make(map[string]bool)
	for _, iv := range intervals {
		intervalMap[[2]string{iv.Begin, iv.End}] = iv.Name
		endSet[iv.End] = true
	}
	ends := make([]string, 0, len(endSet))
	for e := range endSet {
		ends = append(ends, "'"+e+"'")
	}
	endsSQL := "(" + joinStrings(ends, ",") + ")"

	type metricRow struct {
		FundCode      string
		IntervalBegin string
		IntervalEnd   string
		MetricName    string
		MetricValue   float64
	}
	type infoRow struct {
		ProdCode  string
		ProdName  string
		ProdComp  string
		ProdType  string
		Scale     string
		NavSource string
		Fid       sql.NullInt64
	}

	var wg sync.WaitGroup
	var metrics []metricRow
	var infos []infoRow
	startDates := make(map[string]string)
	var errMetrics, errInfo, errStart error

	wg.Add(3)
	go func() {
		defer wg.Done()
		rows, err := navDB.Query(
			"SELECT fund_code, DATE_FORMAT(interval_begin,'%Y-%m-%d'), DATE_FORMAT(interval_end,'%Y-%m-%d'), metric_name, metric_value " +
				"FROM nav_interval_metrics WHERE is_excess=0 AND metric_name IN ('return','sharpe','MDD') " +
				"AND DATE(interval_end) IN " + endsSQL)
		if err != nil {
			errMetrics = err
			return
		}
		defer rows.Close()
		for rows.Next() {
			var r metricRow
			if e := rows.Scan(&r.FundCode, &r.IntervalBegin, &r.IntervalEnd, &r.MetricName, &r.MetricValue); e == nil {
				metrics = append(metrics, r)
			}
		}
	}()
	go func() {
		defer wg.Done()
		rows, err := euclidDB.Query(
			"SELECT prod_code, prod_name, prod_comp, prod_type, 管理人规模, 净值来源, fid FROM fund_basic_info WHERE 净值来源 IS NOT NULL")
		if err != nil {
			errInfo = err
			return
		}
		defer rows.Close()
		for rows.Next() {
			var r infoRow
			if e := rows.Scan(&r.ProdCode, &r.ProdName, &r.ProdComp, &r.ProdType, &r.Scale, &r.NavSource, &r.Fid); e == nil {
				infos = append(infos, r)
			}
		}
	}()
	go func() {
		defer wg.Done()
		rows, err := navDB.Query("SELECT register_number, MIN(date) FROM nav_data GROUP BY register_number")
		if err != nil {
			errStart = err
			return
		}
		defer rows.Close()
		for rows.Next() {
			var reg string
			var d time.Time
			if e := rows.Scan(&reg, &d); e == nil {
				startDates[reg] = d.Format("2006-01-02")
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
	if errStart != nil {
		return nil, errStart
	}

	// pivot metrics: fund_code -> interval_metric -> value
	type pivotKey struct{ fund, iv, metric string }
	pivotMap := make(map[pivotKey]float64)
	for _, m := range metrics {
		ivName, ok := intervalMap[[2]string{m.IntervalBegin, m.IntervalEnd}]
		if !ok {
			continue
		}
		pivotMap[pivotKey{m.FundCode, ivName, m.MetricName}] = m.MetricValue
	}

	get := func(fund, iv, metric string) *float64 {
		v, ok := pivotMap[pivotKey{fund, iv, metric}]
		if !ok {
			return nil
		}
		return &v
	}

	var funds []Fund
	for _, info := range infos {
		code := info.ProdCode
		startKey := code
		if info.NavSource == "个人净值" && info.Fid.Valid {
			startKey = fmt.Sprintf("p_%d", info.Fid.Int64)
		}
		startDate := startDates[startKey]
		if startDate == "" {
			startDate = "-"
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
			StartDate:        startDate,
			RecentWeek:       fmtVal(get(code, "recent_week", "return"), true),
			Ytd:              fmtVal(get(code, "ytd", "return"), true),
			RecentYear:       fmtVal(get(code, "recent_year", "return"), true),
			RecentYearSharpe: fmtVal(get(code, "recent_year", "sharpe"), false),
			RecentYearMdd:    fmtVal(get(code, "recent_year", "MDD"), true),
			Y2025:            fmtVal(get(code, "y2025", "return"), true),
			Y2024:            fmtVal(get(code, "y2024", "return"), true),
			Y2023:            fmtVal(get(code, "y2023", "return"), true),
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

func joinStrings(ss []string, sep string) string {
	return strings.Join(ss, sep)
}
