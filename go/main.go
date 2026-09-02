package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

type Config struct {
	DBHost  string `json:"db_host"`
	DBPort  string `json:"db_port"`
	DBUser  string `json:"db_user"`
	DBPass  string `json:"db_pass"`
	LastDay string `json:"last_day"`
}

type IntervalsFile struct {
	Yearly []Interval `json:"yearly"`
}

type intervalState struct {
	All   []Interval
	Week  Interval
	Month Interval
	YTD   Interval
}

type dataSettingsResponse struct {
	LastDay    string `json:"last_day"`
	WeekBegin  string `json:"week_begin"`
	WeekEnd    string `json:"week_end"`
	MonthBegin string `json:"month_begin"`
	MonthEnd   string `json:"month_end"`
	YTDBegin   string `json:"ytd_begin"`
	YTDEnd     string `json:"ytd_end"`
	FundCount  int    `json:"fund_count,omitempty"`
}

var (
	errInvalidLastDay = errors.New("invalid last_day")
	errLastDayNoData  = errors.New("last_day has no data")
)

var (
	cfg       Config
	intervals []Interval
	weekIV    Interval
	monthIV   Interval
	ytdIV     Interval
	hasConfig bool
	dataDir   string
)

func resolveDataDir(exePath string) string {
	exeDir := filepath.Dir(exePath)
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	candidates := []string{cwd, filepath.Dir(cwd), exeDir}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "config.json")); err == nil {
			return dir
		}
	}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		testFile := filepath.Join(dir, ".write_test")
		if f, err := os.Create(testFile); err == nil {
			f.Close()
			_ = os.Remove(testFile)
			return dir
		}
	}
	appData, err := os.UserConfigDir()
	if err != nil {
		appData = os.TempDir()
	}
	dir := filepath.Join(appData, "pvt_prod_track")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

func main() {
	listenAddr := flag.String("listen", "127.0.0.1:5003", "listen address for service mode")
	dataDirFlag := flag.String("data-dir", "", "data directory for runtime state files")
	resetAdmin := flag.Bool("reset-admin", false, "reset the web service administrator password")
	flag.Parse()

	explicitDataDir := ""
	if *dataDirFlag != "" {
		var err error
		explicitDataDir, err = filepath.Abs(*dataDirFlag)
		if err != nil {
			log.Fatal(err)
		}
	}

	exePath, _ := os.Executable()
	if exePath != "" {
		_ = os.Chdir(filepath.Dir(exePath))
	}
	if explicitDataDir != "" {
		if err := os.MkdirAll(explicitDataDir, 0700); err != nil {
			log.Fatal(err)
		}
		dataDir = explicitDataDir
	} else {
		dataDir = resolveDataDir(exePath)
	}

	cfgData, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err == nil {
		_ = json.Unmarshal(cfgData, &cfg)
		if cfg.DBPort == "" {
			cfg.DBPort = "3306"
		}
		hasConfig = true
		_ = reloadIntervals()
		_ = initDBPools(&cfg)
		go loadData(&cfg, intervals)
	}

	if runtime.GOOS != "windows" || isWebServiceExe(exePath) {
		access, err := newAccessControl(dataDir, *resetAdmin)
		if err != nil {
			log.Fatal(err)
		}
		feedback, err := newFeedbackStore(dataDir)
		if err != nil {
			log.Fatal(err)
		}
		startServiceServer(*listenAddr, access, feedback)
		return
	}
	startDesktopApp()
}

func isWebServiceExe(exePath string) bool {
	base := filepath.Base(exePath)
	return base == "pvt_prod_track_web.exe" || base == "pvt_prod_track_web"
}

func startServiceServer(addr string, access *accessControl, feedback *feedbackStore) {
	srv := &http.Server{Addr: addr, Handler: newServiceMux(access, feedback)}
	log.Printf("service server listening on http://%s", addr)
	log.Fatal(srv.ListenAndServe())
}

func newAppMux(templatePath string) *http.ServeMux {
	staticFS, _ := fs.Sub(embeddedAssets, "assets/static")
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serveTemplate(w, templatePath)
	})
	mux.HandleFunc("/api/data", handleData)
	mux.HandleFunc("/api/strategies", handleStrategies)
	mux.HandleFunc("/api/refresh", handleRefresh)
	mux.HandleFunc("/api/export/excel", handleExcel)
	mux.HandleFunc("/api/intervals", handleIntervals)
	mux.HandleFunc("/api/config", handleConfig)
	mux.HandleFunc("/api/config/holiday", handleHolidayUpload)
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"configured": hasConfig})
	})
	mux.HandleFunc("/icon.ico", func(w http.ResponseWriter, r *http.Request) {
		data, err := embeddedAssets.ReadFile("assets/icon.ico")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/x-icon")
		_, _ = w.Write(data)
	})
	return mux
}

func serveTemplate(w http.ResponseWriter, path string) {
	data, err := embeddedAssets.ReadFile(path)
	if err != nil {
		http.Error(w, "template not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func loadIntervalState(lastDayValue string) (intervalState, error) {
	if lastDayValue == "" {
		return intervalState{}, nil
	}
	lastDay, err := time.Parse("2006-01-02", lastDayValue)
	if err != nil {
		return intervalState{}, errInvalidLastDay
	}

	ivData, err := os.ReadFile(filepath.Join(dataDir, "intervals.json"))
	if err != nil {
		ivData, err = embeddedAssets.ReadFile("assets/intervals.json")
		if err != nil {
			return intervalState{}, err
		}
	}
	var ivCfg IntervalsFile
	if err := json.Unmarshal(ivData, &ivCfg); err != nil {
		return intervalState{}, err
	}

	holidays, err := loadHolidays(filepath.Join(dataDir, "Chinese_special_holiday.txt"))
	if err != nil {
		hData, err2 := embeddedAssets.ReadFile("assets/Chinese_special_holiday.txt")
		if err2 != nil {
			return intervalState{}, err
		}
		holidays, err = loadHolidaysFromBytes(hData)
		if err != nil {
			return intervalState{}, err
		}
	}

	state := intervalState{All: buildIntervals(lastDay, ivCfg.Yearly, holidays)}
	for _, iv := range state.All {
		if iv.Name == "recent_week" {
			state.Week = iv
		}
		if iv.Name == "recent_month" {
			state.Month = iv
		}
		if iv.Name == "ytd" {
			state.YTD = iv
		}
	}
	return state, nil
}

func applyIntervalState(state intervalState) {
	intervals = state.All
	weekIV = state.Week
	monthIV = state.Month
	ytdIV = state.YTD
}

func reloadIntervals() error {
	state, err := loadIntervalState(cfg.LastDay)
	if err != nil {
		return err
	}
	applyIntervalState(state)
	return nil
}

func dataSettings(lastDay string, state intervalState, fundCount int) dataSettingsResponse {
	return dataSettingsResponse{
		LastDay:    lastDay,
		WeekBegin:  state.Week.Begin,
		WeekEnd:    state.Week.End,
		MonthBegin: state.Month.Begin,
		MonthEnd:   state.Month.End,
		YTDBegin:   state.YTD.Begin,
		YTDEnd:     state.YTD.End,
		FundCount:  fundCount,
	}
}

func currentDataSettings() dataSettingsResponse {
	return dataSettings(cfg.LastDay, intervalState{All: intervals, Week: weekIV, Month: monthIV, YTD: ytdIV}, 0)
}

func updateLastDay(lastDay string) (dataSettingsResponse, error) {
	return updateLastDayWithLoader(lastDay, loadDataUncached)
}

func updateLastDayWithLoader(lastDay string, loader func([]Interval) ([]Fund, error)) (dataSettingsResponse, error) {
	state, err := loadIntervalState(lastDay)
	if err != nil {
		return dataSettingsResponse{}, err
	}
	if lastDay == "" {
		return dataSettingsResponse{}, errInvalidLastDay
	}

	funds, err := loader(state.All)
	if err != nil {
		return dataSettingsResponse{}, err
	}
	if len(funds) == 0 {
		return dataSettingsResponse{}, errLastDayNoData
	}

	newCfg := cfg
	newCfg.LastDay = lastDay
	data, err := json.MarshalIndent(newCfg, "", "  ")
	if err != nil {
		return dataSettingsResponse{}, err
	}
	configPath := filepath.Join(dataDir, "config.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return dataSettingsResponse{}, err
	}
	_ = os.Chmod(configPath, 0600)

	cfg = newCfg
	hasConfig = true
	applyIntervalState(state)
	replaceCache(funds)
	return dataSettings(lastDay, state, len(funds)), nil
}

func handleAdminDataSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, currentDataSettings())
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		LastDay string `json:"last_day"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求内容无效")
		return
	}
	result, err := updateLastDay(input.LastDay)
	if errors.Is(err, errInvalidLastDay) {
		writeAPIError(w, http.StatusBadRequest, "invalid_last_day", "请输入有效的交易日，格式为 YYYY-MM-DD")
		return
	}
	if errors.Is(err, errLastDayNoData) {
		writeAPIError(w, http.StatusConflict, "last_day_no_data", "该交易日尚无可展示的指标数据，线上截止日未修改")
		return
	}
	if err != nil {
		log.Printf("更新最新交易日失败: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "last_day_update_failed", "验证或保存失败，线上截止日未修改")
		return
	}
	writeJSON(w, result)
}

func handleData(w http.ResponseWriter, r *http.Request) {
	if !hasConfig {
		writeJSON(w, map[string]any{"funds": []any{}})
		return
	}
	funds, err := loadData(&cfg, intervals)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	message := ""
	if len(funds) == 0 {
		message = noDataMessage()
	}
	strategy := r.URL.Query().Get("strategy")
	result := funds
	if strategy != "" && strategy != "all" {
		result = []Fund{}
		for _, f := range funds {
			if f.Strategy == strategy {
				result = append(result, f)
			}
		}
	}
	if result == nil {
		result = []Fund{}
	}
	resp := map[string]any{"funds": result}
	if message != "" {
		resp["message"] = message
	}
	writeJSON(w, resp)
}

func noDataMessage() string {
	if cfg.LastDay == "" {
		return "未设置最新交易日 last_day，请先在设置中填写后刷新。"
	}
	if weekIV.Begin != "" && weekIV.End != "" {
		return fmt.Sprintf("最新交易日 %s 暂无近一周指标数据（区间 %s 至 %s）。请确认 Nav.nav_interval_metrics 已生成该交易日数据，或将 last_day 调整为已有数据的交易日后刷新。", cfg.LastDay, weekIV.Begin, weekIV.End)
	}
	return fmt.Sprintf("最新交易日 %s 暂无指标数据。请确认数据已生成，或将 last_day 调整为已有数据的交易日后刷新。", cfg.LastDay)
}

func handleStrategies(w http.ResponseWriter, r *http.Request) {
	if !hasConfig {
		writeJSON(w, map[string]any{"strategies": []any{}})
		return
	}
	funds, err := loadData(&cfg, intervals)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	seen := make(map[string]bool)
	for _, f := range funds {
		seen[f.Strategy] = true
	}
	strategies := make([]string, 0, len(seen))
	for s := range seen {
		strategies = append(strategies, s)
	}
	sort.Strings(strategies)
	writeJSON(w, map[string]any{"strategies": strategies})
}

func handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	clearCache()
	if hasConfig {
		if _, err := loadData(&cfg, intervals); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

func handleExcel(w http.ResponseWriter, r *http.Request) {
	funds, err := loadData(&cfg, intervals)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	filename := excelFilename()
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filename))
	if err := exportExcel(funds, w); err != nil {
		log.Println("excel export error:", err)
	}
}

func handleIntervals(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"week_begin":  weekIV.Begin,
		"week_end":    weekIV.End,
		"month_begin": monthIV.Begin,
		"month_end":   monthIV.End,
		"ytd_begin":   ytdIV.Begin,
		"ytd_end":     ytdIV.End,
	})
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, cfg)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var newCfg Config
	if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}
	if newCfg.DBPort == "" {
		newCfg.DBPort = "3306"
	}
	data, _ := json.MarshalIndent(newCfg, "", "  ")
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), data, 0644); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	cfg = newCfg
	hasConfig = true
	clearCache()
	if err := initDBPools(&cfg); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := reloadIntervals(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

func handleHolidayUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	_ = r.ParseMultipartForm(1 << 20)
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", 400)
		return
	}
	defer file.Close()
	data, _ := io.ReadAll(file)
	if err := os.WriteFile(filepath.Join(dataDir, "Chinese_special_holiday.txt"), data, 0644); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	clearCache()
	_ = reloadIntervals()
	writeJSON(w, map[string]any{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
