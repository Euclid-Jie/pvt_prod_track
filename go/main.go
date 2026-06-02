package main

import (
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	webview "github.com/jchv/go-webview2"
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

var (
	cfg       Config
	intervals []Interval
	weekIV    Interval
	ytdIV     Interval
	hasConfig bool
	dataDir   string // directory where config.json is stored
)

// configDir returns a writable directory for config files.
// Prefers exe directory; falls back to %APPDATA%\pvt_prod_track.
func initDataDir() string {
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)
	testFile := filepath.Join(exeDir, ".write_test")
	if f, err := os.Create(testFile); err == nil {
		f.Close()
		os.Remove(testFile)
		return exeDir
	}
	appData, err := os.UserConfigDir()
	if err != nil {
		appData = os.TempDir()
	}
	dir := filepath.Join(appData, "pvt_prod_track")
	os.MkdirAll(dir, 0755)
	return dir
}

func main() {
	exePath, err := os.Executable()
	if err == nil {
		os.Chdir(filepath.Dir(exePath))
	}
	dataDir = initDataDir()

	// Try loading config — if missing, open settings page first
	cfgData, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err == nil {
		json.Unmarshal(cfgData, &cfg)
		if cfg.DBPort == "" {
			cfg.DBPort = "3306"
		}
		hasConfig = true
		reloadIntervals()
		initDBPools(&cfg)
		go loadData(&cfg, intervals)
	}

	staticFS, _ := fs.Sub(embeddedAssets, "assets/static")
	templatesFS, _ := fs.Sub(embeddedAssets, "assets/templates")
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	http.Handle("/", http.FileServer(http.FS(templatesFS)))
	http.HandleFunc("/api/data", handleData)
	http.HandleFunc("/api/strategies", handleStrategies)
	http.HandleFunc("/api/refresh", handleRefresh)
	http.HandleFunc("/api/export/excel", handleExcel)
	http.HandleFunc("/api/intervals", handleIntervals)
	http.HandleFunc("/api/config", handleConfig)
	http.HandleFunc("/api/config/holiday", handleHolidayUpload)
	http.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"configured": hasConfig})
	})
	http.HandleFunc("/icon.ico", func(w http.ResponseWriter, r *http.Request) {
		data, err := embeddedAssets.ReadFile("assets/icon.ico")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/x-icon")
		w.Write(data)
	})

	go func() {
		if err := http.ListenAndServe(":5000", nil); err != nil {
			log.Fatal(err)
		}
	}()

	w := webview.New(false)
	if w == nil {
		log.Fatal("WebView2 初始化失败，请确认系统已安装 WebView2 Runtime")
	}
	defer w.Destroy()
	w.SetTitle("私募产品周报")
	w.SetSize(1400, 860, webview.HintNone)
	setWindowIcon(w.Window())
	w.Navigate("http://localhost:5000")
	w.Run()
}

func reloadIntervals() error {
	// Prefer local file (user-updated), fall back to embedded
	ivData, err := os.ReadFile(filepath.Join(dataDir, "intervals.json"))
	if err != nil {
		ivData, err = embeddedAssets.ReadFile("assets/intervals.json")
		if err != nil {
			return err
		}
	}
	var ivCfg IntervalsFile
	if err := json.Unmarshal(ivData, &ivCfg); err != nil {
		return err
	}

	// Prefer local holiday file, fall back to embedded
	holidays, err := loadHolidays(filepath.Join(dataDir, "Chinese_special_holiday.txt"))
	if err != nil {
		hData, err2 := embeddedAssets.ReadFile("assets/Chinese_special_holiday.txt")
		if err2 != nil {
			return err
		}
		holidays, err = loadHolidaysFromBytes(hData)
		if err != nil {
			return err
		}
	}

	if cfg.LastDay == "" {
		return nil
	}
	lastDay, err := time.Parse("2006-01-02", cfg.LastDay)
	if err != nil {
		return err
	}
	intervals = buildIntervals(lastDay, ivCfg.Yearly, holidays)
	for _, iv := range intervals {
		if iv.Name == "recent_week" {
			weekIV = iv
		}
		if iv.Name == "ytd" {
			ytdIV = iv
		}
	}
	return nil
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
	strategy := r.URL.Query().Get("strategy")
	result := funds
	if strategy != "" && strategy != "all" {
		result = nil
		for _, f := range funds {
			if f.Strategy == strategy {
				result = append(result, f)
			}
		}
	}
	writeJSON(w, map[string]any{"funds": result})
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
		"week_begin": weekIV.Begin,
		"week_end":   weekIV.End,
		"ytd_begin":  ytdIV.Begin,
		"ytd_end":    ytdIV.End,
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
	r.ParseMultipartForm(1 << 20)
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
	reloadIntervals()
	writeJSON(w, map[string]any{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
