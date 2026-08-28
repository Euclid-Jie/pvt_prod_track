package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	deviceCookieName = "pvt_device"
	adminCookieName  = "pvt_admin"
)

type applicationView struct {
	ID        string     `json:"id"`
	DeviceID  string     `json:"device_id"`
	IP        string     `json:"ip"`
	Name      string     `json:"name"`
	Contact   string     `json:"contact,omitempty"`
	Message   string     `json:"message"`
	Status    string     `json:"status"`
	Decision  string     `json:"decision,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
}

type allowEntryView struct {
	ID                  string     `json:"id"`
	Kind                string     `json:"kind"`
	Value               string     `json:"value"`
	Label               string     `json:"label"`
	SourceApplicationID string     `json:"source_application_id,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	RevokedAt           *time.Time `json:"revoked_at,omitempty"`
}

func newServiceMux(ac *accessControl) http.Handler {
	appMux := newAppMux("assets/templates/service.html")
	mux := http.NewServeMux()

	mux.Handle("/static/", appMux)
	mux.Handle("/icon.ico", appMux)
	mux.Handle("GET /access", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		_, deviceHash, err := ac.ensureDevice(w, r)
		if err != nil {
			http.Error(w, "无法创建设备凭证", http.StatusInternalServerError)
			return
		}
		if ac.sessionValid(adminSessionToken(r)) || ac.isAllowed(deviceHash, clientIP(r)) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		serveTemplate(w, "assets/templates/access.html")
	}))
	mux.Handle("GET /api/access/status", http.HandlerFunc(ac.handleAccessStatus))
	mux.Handle("POST /api/access/applications", http.HandlerFunc(ac.handleCreateApplication))

	localPage := func(path string) http.Handler {
		return ac.directLocalOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			serveTemplate(w, path)
		}))
	}
	mux.Handle("GET /admin", localPage("assets/templates/admin.html"))
	mux.Handle("GET /admin/setup", localPage("assets/templates/admin.html"))
	mux.Handle("GET /api/admin/session", ac.directLocalOnly(http.HandlerFunc(ac.handleAdminSession)))
	mux.Handle("POST /api/admin/setup", ac.directLocalOnly(http.HandlerFunc(ac.handleAdminSetup)))
	mux.Handle("POST /api/admin/login", ac.directLocalOnly(http.HandlerFunc(ac.handleAdminLogin)))
	mux.Handle("POST /api/admin/logout", ac.requireAdmin(http.HandlerFunc(ac.handleAdminLogout)))
	mux.Handle("GET /api/admin/applications", ac.requireAdmin(http.HandlerFunc(ac.handleListApplications)))
	mux.Handle("POST /api/admin/applications/{id}/approve", ac.requireAdmin(http.HandlerFunc(ac.handleApproveApplication)))
	mux.Handle("POST /api/admin/applications/{id}/reject", ac.requireAdmin(http.HandlerFunc(ac.handleRejectApplication)))
	mux.Handle("DELETE /api/admin/applications/{id}", ac.requireAdmin(http.HandlerFunc(ac.handleDeleteApplication)))
	mux.Handle("GET /api/admin/allowlist", ac.requireAdmin(http.HandlerFunc(ac.handleListAllowlist)))
	mux.Handle("POST /api/admin/allowlist/{id}/revoke", ac.requireAdmin(http.HandlerFunc(ac.handleRevokeAllowEntry)))
	mux.Handle("POST /api/admin/password", ac.requireAdmin(http.HandlerFunc(ac.handleChangePassword)))

	// Database credentials and holiday uploads are never exposed to approved viewers.
	mux.Handle("/api/config/holiday", ac.requireAdmin(http.HandlerFunc(handleHolidayUpload)))
	mux.Handle("/api/config", ac.requireAdmin(http.HandlerFunc(handleConfig)))
	mux.Handle("/", ac.requireViewer(appMux))

	return serviceSecurityHeaders(mux)
}

func serviceSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		if requestIsHTTPS(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}

func trustedProxyRequest(r *http.Request) bool {
	ip := remoteIP(r)
	return ip != nil && ip.IsLoopback()
}

func clientIP(r *http.Request) string {
	if trustedProxyRequest(r) {
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(parts) - 1; i >= 0; i-- {
			if ip := canonicalIP(parts[i]); ip != "" {
				return ip
			}
		}
	}
	if ip := remoteIP(r); ip != nil {
		return ip.String()
	}
	return ""
}

func directLocalRequest(r *http.Request) bool {
	ip := remoteIP(r)
	return ip != nil && ip.IsLoopback() && strings.TrimSpace(r.Header.Get("X-Forwarded-For")) == ""
}

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !trustedProxyRequest(r) {
		return false
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(parts[i]), "https") {
			return true
		}
		if strings.TrimSpace(parts[i]) != "" {
			return false
		}
	}
	return false
}

func (ac *accessControl) ensureDevice(w http.ResponseWriter, r *http.Request) (string, string, error) {
	if cookie, err := r.Cookie(deviceCookieName); err == nil && validDeviceToken(cookie.Value) {
		return cookie.Value, tokenHash(cookie.Value), nil
	}
	token, err := randomToken(32)
	if err != nil {
		return "", "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     deviceCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	return token, tokenHash(token), nil
}

func adminSessionToken(r *http.Request) string {
	cookie, err := r.Cookie(adminCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func setAdminCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   8 * 60 * 60,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func clearAdminCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func (ac *accessControl) directLocalOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !directLocalRequest(r) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (ac *accessControl) requireAdmin(next http.Handler) http.Handler {
	return ac.directLocalOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			writeAPIError(w, http.StatusForbidden, "origin_mismatch", "请求来源无效")
			return
		}
		if !ac.sessionValid(adminSessionToken(r)) {
			writeAPIError(w, http.StatusUnauthorized, "admin_login_required", "请先登录管理页面")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	}))
}

func (ac *accessControl) requireViewer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, deviceHash, err := ac.ensureDevice(w, r)
		if err != nil {
			http.Error(w, "无法创建设备凭证", http.StatusInternalServerError)
			return
		}
		if ac.sessionValid(adminSessionToken(r)) || ac.isAllowed(deviceHash, clientIP(r)) {
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeAPIError(w, http.StatusForbidden, "access_required", "该设备尚未获得访问权限")
			return
		}
		http.Redirect(w, r, "/access", http.StatusFound)
	})
}

func sameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(u.Host, r.Host) {
		return false
	}
	wantScheme := "http"
	if requestIsHTTPS(r) {
		wantScheme = "https"
	}
	return strings.EqualFold(u.Scheme, wantScheme)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, target any, limit int64) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeAPIError(w, http.StatusUnsupportedMediaType, "json_required", "请求必须使用 JSON")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求内容格式不正确")
		return false
	}
	return true
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": code, "message": message})
}

func applicationToView(application accessApplication) applicationView {
	return applicationView{
		ID:        application.ID,
		DeviceID:  shortDeviceID(application.DeviceHash),
		IP:        application.IP,
		Name:      application.Name,
		Contact:   application.Contact,
		Message:   application.Message,
		Status:    application.Status,
		Decision:  application.Decision,
		CreatedAt: application.CreatedAt,
		DecidedAt: application.DecidedAt,
	}
}

func allowEntryToView(entry allowEntry) allowEntryView {
	value := entry.Value
	if entry.Kind == "device" {
		value = shortDeviceID(value)
	}
	return allowEntryView{
		ID:                  entry.ID,
		Kind:                entry.Kind,
		Value:               value,
		Label:               entry.Label,
		SourceApplicationID: entry.SourceApplicationID,
		CreatedAt:           entry.CreatedAt,
		RevokedAt:           entry.RevokedAt,
	}
}

func (ac *accessControl) handleAccessStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	_, deviceHash, err := ac.ensureDevice(w, r)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "device_error", "无法创建设备凭证")
		return
	}
	ip := clientIP(r)
	allowed := ac.isAllowed(deviceHash, ip) || ac.sessionValid(adminSessionToken(r))
	response := map[string]any{
		"allowed":   allowed,
		"device_id": shortDeviceID(deviceHash),
		"ip":        ip,
	}
	if application := ac.latestApplication(deviceHash); application != nil {
		view := applicationToView(*application)
		if view.Status == "approved" && !allowed {
			view.Status = "revoked"
		}
		response["application"] = view
	}
	writeJSON(w, response)
}

func (ac *accessControl) handleCreateApplication(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !sameOrigin(r) {
		writeAPIError(w, http.StatusForbidden, "origin_mismatch", "请求来源无效")
		return
	}
	_, deviceHash, err := ac.ensureDevice(w, r)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "device_error", "无法创建设备凭证")
		return
	}
	var input applicationInput
	if !decodeJSONBody(w, r, &input, 8192) {
		return
	}
	application, err := ac.createApplication(deviceHash, clientIP(r), input)
	if errors.Is(err, errPendingApplication) {
		writeJSON(w, map[string]any{"status": "pending", "existing": true, "application": applicationToView(application)})
		return
	}
	if errors.Is(err, errRateLimited) {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "申请过于频繁，请一小时后再试")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_application", err.Error())
		return
	}
	writeJSONStatus(w, http.StatusCreated, map[string]any{"status": "pending", "application": applicationToView(application)})
}

func (ac *accessControl) handleAdminSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{
		"configured":    ac.configured(),
		"authenticated": ac.sessionValid(adminSessionToken(r)),
	})
}

func (ac *accessControl) handleAdminSetup(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeAPIError(w, http.StatusForbidden, "origin_mismatch", "请求来源无效")
		return
	}
	var input struct {
		SetupToken string `json:"setup_token"`
		Password   string `json:"password"`
	}
	if !decodeJSONBody(w, r, &input, 4096) {
		return
	}
	if err := ac.setupAdmin(input.SetupToken, input.Password); err != nil {
		writeAPIError(w, http.StatusBadRequest, "setup_failed", err.Error())
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

func (ac *accessControl) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeAPIError(w, http.StatusForbidden, "origin_mismatch", "请求来源无效")
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &input, 4096) {
		return
	}
	token, err := ac.login(clientIP(r), input.Password)
	if errors.Is(err, errRateLimited) {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "登录失败次数过多，请 15 分钟后再试")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "login_failed", err.Error())
		return
	}
	setAdminCookie(w, r, token)
	writeJSON(w, map[string]any{"status": "ok"})
}

func (ac *accessControl) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	ac.logout(adminSessionToken(r))
	clearAdminCookie(w, r)
	writeJSON(w, map[string]any{"status": "ok"})
}

func (ac *accessControl) handleListApplications(w http.ResponseWriter, r *http.Request) {
	applications := ac.listApplications(r.URL.Query().Get("status"))
	views := make([]applicationView, 0, len(applications))
	for _, application := range applications {
		views = append(views, applicationToView(application))
	}
	writeJSON(w, map[string]any{"applications": views})
}

func (ac *accessControl) handleApproveApplication(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Grant string `json:"grant"`
	}
	if !decodeJSONBody(w, r, &input, 2048) {
		return
	}
	application, err := ac.approveApplication(r.PathValue("id"), input.Grant)
	if err != nil {
		ac.writeAdminOperationError(w, err)
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "application": applicationToView(application)})
}

func (ac *accessControl) handleRejectApplication(w http.ResponseWriter, r *http.Request) {
	application, err := ac.rejectApplication(r.PathValue("id"))
	if err != nil {
		ac.writeAdminOperationError(w, err)
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "application": applicationToView(application)})
}

func (ac *accessControl) handleDeleteApplication(w http.ResponseWriter, r *http.Request) {
	if err := ac.deleteApplication(r.PathValue("id")); err != nil {
		ac.writeAdminOperationError(w, err)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

func (ac *accessControl) handleListAllowlist(w http.ResponseWriter, r *http.Request) {
	entries := ac.listAllowlist()
	views := make([]allowEntryView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, allowEntryToView(entry))
	}
	writeJSON(w, map[string]any{"allowlist": views})
}

func (ac *accessControl) handleRevokeAllowEntry(w http.ResponseWriter, r *http.Request) {
	entry, err := ac.revokeAllowEntry(r.PathValue("id"))
	if err != nil {
		ac.writeAdminOperationError(w, err)
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "entry": allowEntryToView(entry)})
}

func (ac *accessControl) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeJSONBody(w, r, &input, 4096) {
		return
	}
	if err := ac.changePassword(input.CurrentPassword, input.NewPassword); err != nil {
		writeAPIError(w, http.StatusBadRequest, "password_change_failed", err.Error())
		return
	}
	clearAdminCookie(w, r)
	writeJSON(w, map[string]any{"status": "ok", "login_required": true})
}

func (ac *accessControl) writeAdminOperationError(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrNotExist) {
		writeAPIError(w, http.StatusNotFound, "not_found", "记录不存在")
		return
	}
	writeAPIError(w, http.StatusConflict, "operation_failed", err.Error())
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
