package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestAccessControl(t *testing.T) *accessControl {
	t.Helper()
	ac, err := newAccessControl(t.TempDir(), false)
	if err != nil {
		t.Fatalf("newAccessControl() error = %v", err)
	}
	return ac
}

func newTestServiceMux(t *testing.T, ac *accessControl) http.Handler {
	t.Helper()
	feedback, err := newFeedbackStore(t.TempDir())
	if err != nil {
		t.Fatalf("newFeedbackStore() error = %v", err)
	}
	return newServiceMux(ac, feedback)
}

func TestClientIPTrustsOnlyLoopbackProxyAndUsesRightmostValue(t *testing.T) {
	trusted := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	trusted.RemoteAddr = "127.0.0.1:50000"
	trusted.Header.Set("X-Forwarded-For", "198.51.100.10, 203.0.113.22")
	if got := clientIP(trusted); got != "203.0.113.22" {
		t.Fatalf("clientIP(trusted) = %q, want 203.0.113.22", got)
	}

	spoofed := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	spoofed.RemoteAddr = "192.0.2.44:50000"
	spoofed.Header.Set("X-Forwarded-For", "203.0.113.99")
	if got := clientIP(spoofed); got != "192.0.2.44" {
		t.Fatalf("clientIP(spoofed) = %q, want remote address", got)
	}

	ipv6 := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	ipv6.RemoteAddr = "[::1]:50000"
	ipv6.Header.Set("X-Forwarded-For", "2001:db8::8")
	if got := clientIP(ipv6); got != "2001:db8::8" {
		t.Fatalf("clientIP(ipv6) = %q, want 2001:db8::8", got)
	}
}

func TestApplicationApprovalRevocationAndPersistence(t *testing.T) {
	dir := t.TempDir()
	ac, err := newAccessControl(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)
	ac.now = func() time.Time { return now }
	deviceHash := tokenHash("device-one")
	ip := "203.0.113.8"
	application, err := ac.createApplication(deviceHash, ip, applicationInput{Name: "访客甲", Contact: "visitor@example.com", Message: "申请查看周报"})
	if err != nil {
		t.Fatalf("createApplication() error = %v", err)
	}
	if _, err := ac.createApplication(deviceHash, ip, applicationInput{Name: "访客甲", Message: "重复申请"}); !errors.Is(err, errPendingApplication) {
		t.Fatalf("duplicate application error = %v, want errPendingApplication", err)
	}
	approved, err := ac.approveApplication(application.ID, "device")
	if err != nil {
		t.Fatalf("approveApplication() error = %v", err)
	}
	if approved.Status != "approved" || !ac.isAllowed(deviceHash, "198.51.100.9") {
		t.Fatalf("device approval did not grant device access: %+v", approved)
	}
	entries := ac.listAllowlist()
	if len(entries) != 1 || entries[0].Kind != "device" {
		t.Fatalf("allowlist = %+v, want one device entry", entries)
	}
	if _, err := ac.revokeAllowEntry(entries[0].ID); err != nil {
		t.Fatalf("revokeAllowEntry() error = %v", err)
	}
	if ac.isAllowed(deviceHash, ip) {
		t.Fatal("revoked device remains allowed")
	}

	ac.mu.Lock()
	ac.state.AdminPasswordHash = "configured-for-reload-test"
	if err := ac.saveLocked(); err != nil {
		ac.mu.Unlock()
		t.Fatalf("saveLocked() error = %v", err)
	}
	ac.mu.Unlock()

	reloaded, err := newAccessControl(dir, false)
	if err != nil {
		t.Fatalf("reload access control error = %v", err)
	}
	if len(reloaded.listApplications("approved")) != 1 || reloaded.isAllowed(deviceHash, ip) {
		t.Fatal("persisted approval history or revocation was not restored correctly")
	}
}

func TestIPApprovalAllowsOtherDevice(t *testing.T) {
	ac := newTestAccessControl(t)
	ip := "198.51.100.18"
	application, err := ac.createApplication(tokenHash("device-a"), ip, applicationInput{Name: "访客乙", Message: "请批准 IP"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ac.approveApplication(application.ID, "ip"); err != nil {
		t.Fatal(err)
	}
	if !ac.isAllowed(tokenHash("different-device"), ip) {
		t.Fatal("IP approval did not allow another device on the same exact IP")
	}
	if ac.isAllowed(tokenHash("different-device"), "198.51.100.19") {
		t.Fatal("IP approval allowed a different IP")
	}
}

func TestManualIPAllowEntryCanonicalizesPersistsAndRejectsDuplicates(t *testing.T) {
	dir := t.TempDir()
	ac, err := newAccessControl(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	ac.now = func() time.Time {
		return time.Date(2026, 9, 13, 7, 0, 0, 0, time.UTC)
	}

	entry, err := ac.addManualIPAllowEntry(" 2001:0DB8:0:0:0:0:0:1 ", "  手工访客  ")
	if err != nil {
		t.Fatalf("addManualIPAllowEntry() error = %v", err)
	}
	if entry.Kind != "ip" || entry.Value != "2001:db8::1" || entry.Label != "手工访客" || entry.SourceApplicationID != "" {
		t.Fatalf("manual allow entry = %+v", entry)
	}
	if !ac.isAllowed(tokenHash("other-device"), "2001:db8::1") {
		t.Fatal("manual IP allow entry did not grant access")
	}

	if _, err := ac.addManualIPAllowEntry("2001:db8::1", "另一个姓名"); !errors.Is(err, errAllowlistIPExists) {
		t.Fatalf("duplicate manual IP error = %v, want errAllowlistIPExists", err)
	}

	reloaded, err := newAccessControl(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	entries := reloaded.listAllowlist()
	if len(entries) != 1 || entries[0].Value != "2001:db8::1" || entries[0].Label != "手工访客" {
		t.Fatalf("reloaded allowlist = %+v", entries)
	}
}

func TestManualIPAllowlistAdminEndpoint(t *testing.T) {
	ac := newTestAccessControl(t)
	ac.setupTokenHash = tokenHash("manual-ip-setup")
	if err := ac.setupAdmin("manual-ip-setup", "manual-ip-admin-password"); err != nil {
		t.Fatal(err)
	}
	adminToken, err := ac.login("127.0.0.1", "manual-ip-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	serviceMux := newTestServiceMux(t, ac)

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/admin/allowlist", strings.NewReader(`{"ip":"203.0.113.120","name":"手工 IP 用户"}`))
	request.RemoteAddr = "127.0.0.1:50000"
	request.Header.Set("Origin", "http://127.0.0.1")
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminToken})
	result := httptest.NewRecorder()
	serviceMux.ServeHTTP(result, request)
	if result.Code != http.StatusCreated || !strings.Contains(result.Body.String(), `"value":"203.0.113.120"`) || !strings.Contains(result.Body.String(), `"label":"手工 IP 用户"`) {
		t.Fatalf("manual allowlist endpoint = %d %s", result.Code, result.Body.String())
	}

	duplicate := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/admin/allowlist", strings.NewReader(`{"ip":"203.0.113.120","name":"重复用户"}`))
	duplicate.RemoteAddr = "127.0.0.1:50000"
	duplicate.Header.Set("Origin", "http://127.0.0.1")
	duplicate.Header.Set("Content-Type", "application/json")
	duplicate.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminToken})
	duplicateResult := httptest.NewRecorder()
	serviceMux.ServeHTTP(duplicateResult, duplicate)
	if duplicateResult.Code != http.StatusConflict || !strings.Contains(duplicateResult.Body.String(), `"error":"ip_already_allowlisted"`) {
		t.Fatalf("duplicate manual allowlist endpoint = %d %s", duplicateResult.Code, duplicateResult.Body.String())
	}

	unauthorized := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/admin/allowlist", strings.NewReader(`{"ip":"203.0.113.121","name":"未登录用户"}`))
	unauthorized.RemoteAddr = "127.0.0.1:50000"
	unauthorized.Header.Set("Origin", "http://127.0.0.1")
	unauthorized.Header.Set("Content-Type", "application/json")
	unauthorizedResult := httptest.NewRecorder()
	serviceMux.ServeHTTP(unauthorizedResult, unauthorized)
	if unauthorizedResult.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized manual allowlist endpoint = %d, want 401", unauthorizedResult.Code)
	}
}

func TestApplicationRateLimit(t *testing.T) {
	ac := newTestAccessControl(t)
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	ac.now = func() time.Time { return now }
	deviceHash := tokenHash("rate-device")
	for i := 0; i < applicationRateLimit; i++ {
		application, err := ac.createApplication(deviceHash, "203.0.113.30", applicationInput{Name: "频率测试", Message: "申请"})
		if err != nil {
			t.Fatalf("application %d error = %v", i+1, err)
		}
		if _, err := ac.rejectApplication(application.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ac.createApplication(deviceHash, "203.0.113.30", applicationInput{Name: "频率测试", Message: "第六次"}); !errors.Is(err, errRateLimited) {
		t.Fatalf("sixth application error = %v, want errRateLimited", err)
	}
	now = now.Add(time.Hour + time.Second)
	if _, err := ac.createApplication(deviceHash, "203.0.113.30", applicationInput{Name: "频率测试", Message: "限流结束"}); err != nil {
		t.Fatalf("application after limit window error = %v", err)
	}
}

func TestAdminSetupLoginLimitAndSessionExpiry(t *testing.T) {
	ac := newTestAccessControl(t)
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	ac.now = func() time.Time { return now }
	ac.setupTokenHash = tokenHash("one-time-token")
	password := "strong-admin-password"
	if err := ac.setupAdmin("one-time-token", password); err != nil {
		t.Fatalf("setupAdmin() error = %v", err)
	}
	for i := 0; i < loginFailureLimit; i++ {
		if _, err := ac.login("127.0.0.1", "wrong-password"); err == nil || errors.Is(err, errRateLimited) {
			t.Fatalf("failed login %d error = %v", i+1, err)
		}
	}
	if _, err := ac.login("127.0.0.1", password); !errors.Is(err, errRateLimited) {
		t.Fatalf("login after five failures error = %v, want errRateLimited", err)
	}
	now = now.Add(16 * time.Minute)
	token, err := ac.login("127.0.0.1", password)
	if err != nil {
		t.Fatalf("login after lockout error = %v", err)
	}
	if !ac.sessionValid(token) {
		t.Fatal("new admin session is not valid")
	}
	now = now.Add(8*time.Hour + time.Second)
	if ac.sessionValid(token) {
		t.Fatal("expired admin session remains valid")
	}
}

func TestChangePasswordInvalidatesSessions(t *testing.T) {
	ac := newTestAccessControl(t)
	ac.setupTokenHash = tokenHash("setup-password-change")
	if err := ac.setupAdmin("setup-password-change", "original-password-2026"); err != nil {
		t.Fatal(err)
	}
	token, err := ac.login("127.0.0.1", "original-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	if err := ac.changePassword("original-password-2026", "replacement-password-2026"); err != nil {
		t.Fatalf("changePassword() error = %v", err)
	}
	if ac.sessionValid(token) {
		t.Fatal("password change did not invalidate the existing session")
	}
	if _, err := ac.login("127.0.0.1", "original-password-2026"); err == nil {
		t.Fatal("old password still works")
	}
	if _, err := ac.login("127.0.0.1", "replacement-password-2026"); err != nil {
		t.Fatalf("new password login error = %v", err)
	}
}

func TestResetAdminPreservesApplicationsAndAllowlist(t *testing.T) {
	dir := t.TempDir()
	ac, err := newAccessControl(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	ac.setupTokenHash = tokenHash("setup-reset-test")
	if err := ac.setupAdmin("setup-reset-test", "reset-test-password"); err != nil {
		t.Fatal(err)
	}
	application, err := ac.createApplication(tokenHash("reset-device"), "203.0.113.80", applicationInput{Name: "重置测试", Message: "保留记录"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ac.approveApplication(application.ID, "both"); err != nil {
		t.Fatal(err)
	}

	reset, err := newAccessControl(dir, true)
	if err != nil {
		t.Fatalf("newAccessControl(reset) error = %v", err)
	}
	if reset.configured() {
		t.Fatal("reset-admin left the old password configured")
	}
	if len(reset.listApplications("all")) != 1 || len(reset.listAllowlist()) != 2 {
		t.Fatal("reset-admin removed applications or allowlist entries")
	}
}

func TestCorruptAccessStoreFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "access_control.json"), []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newAccessControl(dir, false); err == nil {
		t.Fatal("corrupt access store did not fail closed")
	}
}

func TestHomepageVisitHistoryPersistsAndSkipsLocalOrAPIRequests(t *testing.T) {
	dir := t.TempDir()
	ac, err := newAccessControl(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 31, 3, 0, 0, 0, time.UTC)
	ac.now = func() time.Time { return now }
	deviceToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32))
	deviceHash := tokenHash(deviceToken)
	ac.mu.Lock()
	if err := ac.addAllowEntryLocked("device", deviceHash, "访问历史测试", "", now); err != nil {
		ac.mu.Unlock()
		t.Fatal(err)
	}
	ac.mu.Unlock()
	serviceMux := newTestServiceMux(t, ac)

	approved := httptest.NewRequest(http.MethodGet, "http://report.example/", nil)
	approved.RemoteAddr = "127.0.0.1:50000"
	approved.Header.Set("X-Forwarded-For", "203.0.113.91")
	approved.Header.Set("User-Agent", "Visit Test Browser")
	approved.AddCookie(&http.Cookie{Name: deviceCookieName, Value: deviceToken})
	approvedResult := httptest.NewRecorder()
	serviceMux.ServeHTTP(approvedResult, approved)
	if approvedResult.Code != http.StatusOK {
		t.Fatalf("approved homepage = %d, want 200", approvedResult.Code)
	}

	now = now.Add(time.Minute)
	denied := httptest.NewRequest(http.MethodGet, "http://report.example/", nil)
	denied.RemoteAddr = "127.0.0.1:50000"
	denied.Header.Set("X-Forwarded-For", "203.0.113.92")
	deniedResult := httptest.NewRecorder()
	serviceMux.ServeHTTP(deniedResult, denied)
	if deniedResult.Code != http.StatusFound {
		t.Fatalf("denied homepage = %d, want 302", deniedResult.Code)
	}

	apiRequest := httptest.NewRequest(http.MethodGet, "http://report.example/api/status", nil)
	apiRequest.RemoteAddr = "127.0.0.1:50000"
	apiRequest.Header.Set("X-Forwarded-For", "203.0.113.93")
	serviceMux.ServeHTTP(httptest.NewRecorder(), apiRequest)
	localRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	localRequest.RemoteAddr = "127.0.0.1:50000"
	serviceMux.ServeHTTP(httptest.NewRecorder(), localRequest)

	visits := ac.listVisits()
	if len(visits) != 2 {
		t.Fatalf("visits = %+v, want two public homepage visits", visits)
	}
	if visits[0].IP != "203.0.113.92" || visits[0].Allowed {
		t.Fatalf("latest visit = %+v, want denied visit", visits[0])
	}
	if visits[1].IP != "203.0.113.91" || !visits[1].Allowed || visits[1].UserAgent != "Visit Test Browser" {
		t.Fatalf("earlier visit = %+v, want approved visit", visits[1])
	}

	reloaded, err := newAccessControl(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.listVisits(); len(got) != 2 || got[0].IP != "203.0.113.92" {
		t.Fatalf("reloaded visits = %+v", got)
	}
}

func TestVisitIdentityPrefersDeviceAllowlistAndIncludesApplicant(t *testing.T) {
	ac := newTestAccessControl(t)
	ip := "203.0.113.101"
	targetDevice := tokenHash("identity-device")
	ipApplication, err := ac.createApplication(tokenHash("other-device"), ip, applicationInput{
		Name: "共享 IP 申请人", Message: "通过 IP 访问",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ac.approveApplication(ipApplication.ID, "ip"); err != nil {
		t.Fatal(err)
	}
	deviceApplication, err := ac.createApplication(targetDevice, "198.51.100.20", applicationInput{
		Name: "设备申请人", Contact: "device@example.com", Message: "通过固定设备访问",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ac.approveApplication(deviceApplication.ID, "device"); err != nil {
		t.Fatal(err)
	}

	visit := accessVisit{DeviceHash: targetDevice, IP: ip}
	identity := ac.visitIdentity(visit)
	if identity.Name != "设备申请人" || identity.Contact != "device@example.com" || identity.Message != "通过固定设备访问" {
		t.Fatalf("visit identity = %+v", identity)
	}
	if identity.MatchedBy != "device" || !identity.AllowlistActive {
		t.Fatalf("visit identity match = %+v, want active device match", identity)
	}

	for _, entry := range ac.listAllowlist() {
		if entry.Kind == "device" {
			if _, err := ac.revokeAllowEntry(entry.ID); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	identity = ac.visitIdentity(visit)
	if identity.Name != "设备申请人" || identity.MatchedBy != "device" || identity.AllowlistActive {
		t.Fatalf("revoked visit identity = %+v, want retained revoked device identity", identity)
	}
}

func TestServiceRouteMatrixAndDesktopRemainsOpen(t *testing.T) {
	ac := newTestAccessControl(t)
	deviceToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	deviceHash := tokenHash(deviceToken)
	ac.mu.Lock()
	if err := ac.addAllowEntryLocked("device", deviceHash, "测试设备", "", time.Now().UTC()); err != nil {
		ac.mu.Unlock()
		t.Fatal(err)
	}
	ac.mu.Unlock()
	serviceMux := newTestServiceMux(t, ac)

	unauthorized := httptest.NewRequest(http.MethodGet, "http://report.example/api/status", nil)
	unauthorized.RemoteAddr = "127.0.0.1:50000"
	unauthorized.Header.Set("X-Forwarded-For", "203.0.113.50")
	unauthorizedResult := httptest.NewRecorder()
	serviceMux.ServeHTTP(unauthorizedResult, unauthorized)
	if unauthorizedResult.Code != http.StatusForbidden {
		t.Fatalf("unauthorized /api/status = %d, want 403", unauthorizedResult.Code)
	}

	approved := httptest.NewRequest(http.MethodGet, "http://report.example/api/status", nil)
	approved.RemoteAddr = "127.0.0.1:50000"
	approved.Header.Set("X-Forwarded-For", "203.0.113.50")
	approved.AddCookie(&http.Cookie{Name: deviceCookieName, Value: deviceToken})
	approvedResult := httptest.NewRecorder()
	serviceMux.ServeHTTP(approvedResult, approved)
	if approvedResult.Code != http.StatusOK {
		t.Fatalf("approved /api/status = %d, want 200", approvedResult.Code)
	}

	unauthorizedFeedbackPage := httptest.NewRequest(http.MethodGet, "http://report.example/feedback", nil)
	unauthorizedFeedbackPage.RemoteAddr = "127.0.0.1:50000"
	unauthorizedFeedbackPage.Header.Set("X-Forwarded-For", "203.0.113.51")
	unauthorizedFeedbackResult := httptest.NewRecorder()
	serviceMux.ServeHTTP(unauthorizedFeedbackResult, unauthorizedFeedbackPage)
	if unauthorizedFeedbackResult.Code != http.StatusFound || unauthorizedFeedbackResult.Header().Get("Location") != "/access" {
		t.Fatalf("unauthorized /feedback = %d location %q, want 302 /access", unauthorizedFeedbackResult.Code, unauthorizedFeedbackResult.Header().Get("Location"))
	}

	approvedFeedbackPage := httptest.NewRequest(http.MethodGet, "http://report.example/feedback", nil)
	approvedFeedbackPage.RemoteAddr = "127.0.0.1:50000"
	approvedFeedbackPage.Header.Set("X-Forwarded-For", "203.0.113.50")
	approvedFeedbackPage.AddCookie(&http.Cookie{Name: deviceCookieName, Value: deviceToken})
	approvedFeedbackResult := httptest.NewRecorder()
	serviceMux.ServeHTTP(approvedFeedbackResult, approvedFeedbackPage)
	if approvedFeedbackResult.Code != http.StatusOK || !strings.Contains(approvedFeedbackResult.Body.String(), "让周报更好用") {
		t.Fatalf("approved /feedback = %d body %q, want feedback page", approvedFeedbackResult.Code, approvedFeedbackResult.Body.String())
	}

	approvedReportPage := httptest.NewRequest(http.MethodGet, "http://report.example/", nil)
	approvedReportPage.RemoteAddr = "127.0.0.1:50000"
	approvedReportPage.Header.Set("X-Forwarded-For", "203.0.113.50")
	approvedReportPage.AddCookie(&http.Cookie{Name: deviceCookieName, Value: deviceToken})
	approvedReportResult := httptest.NewRecorder()
	serviceMux.ServeHTTP(approvedReportResult, approvedReportPage)
	if approvedReportResult.Code != http.StatusOK {
		t.Fatalf("approved / = %d, want 200", approvedReportResult.Code)
	}
	if body := approvedReportResult.Body.String(); strings.Contains(body, "id=\"feedbackForm\"") || !strings.Contains(body, "href=\"/feedback\"") {
		t.Fatalf("approved / feedback integration is incorrect")
	}

	accessPage := httptest.NewRequest(http.MethodGet, "http://report.example/access", nil)
	accessPage.RemoteAddr = "127.0.0.1:50000"
	accessPage.Header.Set("X-Forwarded-For", "203.0.113.50")
	accessPage.AddCookie(&http.Cookie{Name: deviceCookieName, Value: deviceToken})
	accessPageResult := httptest.NewRecorder()
	serviceMux.ServeHTTP(accessPageResult, accessPage)
	if accessPageResult.Code != http.StatusFound || accessPageResult.Header().Get("Location") != "/" {
		t.Fatalf("approved /access = %d location %q, want 302 /", accessPageResult.Code, accessPageResult.Header().Get("Location"))
	}

	unauthorizedAccessPage := httptest.NewRequest(http.MethodGet, "http://report.example/access", nil)
	unauthorizedAccessPage.RemoteAddr = "127.0.0.1:50000"
	unauthorizedAccessPage.Header.Set("X-Forwarded-For", "203.0.113.51")
	unauthorizedAccessPageResult := httptest.NewRecorder()
	serviceMux.ServeHTTP(unauthorizedAccessPageResult, unauthorizedAccessPage)
	if unauthorizedAccessPageResult.Code != http.StatusOK {
		t.Fatalf("unauthorized /access = %d, want 200", unauthorizedAccessPageResult.Code)
	}

	configRequest := httptest.NewRequest(http.MethodGet, "http://report.example/api/config", nil)
	configRequest.RemoteAddr = "127.0.0.1:50000"
	configRequest.Header.Set("X-Forwarded-For", "203.0.113.50")
	configRequest.AddCookie(&http.Cookie{Name: deviceCookieName, Value: deviceToken})
	configResult := httptest.NewRecorder()
	serviceMux.ServeHTTP(configResult, configRequest)
	if configResult.Code != http.StatusNotFound {
		t.Fatalf("approved viewer /api/config = %d, want 404", configResult.Code)
	}

	dataSettingsRequest := httptest.NewRequest(http.MethodGet, "http://report.example/api/admin/data-settings", nil)
	dataSettingsRequest.RemoteAddr = "127.0.0.1:50000"
	dataSettingsRequest.Header.Set("X-Forwarded-For", "203.0.113.50")
	dataSettingsRequest.AddCookie(&http.Cookie{Name: deviceCookieName, Value: deviceToken})
	dataSettingsResult := httptest.NewRecorder()
	serviceMux.ServeHTTP(dataSettingsResult, dataSettingsRequest)
	if dataSettingsResult.Code != http.StatusNotFound {
		t.Fatalf("approved viewer /api/admin/data-settings = %d, want 404", dataSettingsResult.Code)
	}

	localDataSettingsRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/admin/data-settings", nil)
	localDataSettingsRequest.RemoteAddr = "127.0.0.1:50000"
	localDataSettingsResult := httptest.NewRecorder()
	serviceMux.ServeHTTP(localDataSettingsResult, localDataSettingsRequest)
	if localDataSettingsResult.Code != http.StatusUnauthorized {
		t.Fatalf("local unauthenticated /api/admin/data-settings = %d, want 401", localDataSettingsResult.Code)
	}

	desktopResult := httptest.NewRecorder()
	desktopRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/status", nil)
	newAppMux("assets/templates/index.html").ServeHTTP(desktopResult, desktopRequest)
	if desktopResult.Code != http.StatusOK {
		t.Fatalf("desktop /api/status = %d, want 200", desktopResult.Code)
	}
}

func TestDeviceCookieSecureOnlyForHTTPS(t *testing.T) {
	ac := newTestAccessControl(t)
	httpsRequest := httptest.NewRequest(http.MethodGet, "http://report.example/api/access/status", nil)
	httpsRequest.RemoteAddr = "127.0.0.1:50000"
	httpsRequest.Header.Set("X-Forwarded-For", "203.0.113.60")
	httpsRequest.Header.Set("X-Forwarded-Proto", "https")
	httpsResult := httptest.NewRecorder()
	newTestServiceMux(t, ac).ServeHTTP(httpsResult, httpsRequest)
	if cookie := httpsResult.Header().Get("Set-Cookie"); !strings.Contains(cookie, "Secure") {
		t.Fatalf("HTTPS device cookie missing Secure: %q", cookie)
	}

	httpRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/access/status", nil)
	httpRequest.RemoteAddr = "127.0.0.1:50000"
	httpResult := httptest.NewRecorder()
	newTestServiceMux(t, ac).ServeHTTP(httpResult, httpRequest)
	if cookie := httpResult.Header().Get("Set-Cookie"); strings.Contains(cookie, "Secure") {
		t.Fatalf("local HTTP device cookie unexpectedly Secure: %q", cookie)
	}
}

func TestRevokedApprovalIsReportedAsRevokedToVisitor(t *testing.T) {
	ac := newTestAccessControl(t)
	deviceToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	application, err := ac.createApplication(tokenHash(deviceToken), "203.0.113.70", applicationInput{Name: "撤销测试", Message: "申请"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ac.approveApplication(application.ID, "device"); err != nil {
		t.Fatal(err)
	}
	entry := ac.listAllowlist()[0]
	if _, err := ac.revokeAllowEntry(entry.ID); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "https://report.example/api/access/status", nil)
	request.RemoteAddr = "127.0.0.1:50000"
	request.Header.Set("X-Forwarded-For", "203.0.113.70")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.AddCookie(&http.Cookie{Name: deviceCookieName, Value: deviceToken})
	result := httptest.NewRecorder()
	newTestServiceMux(t, ac).ServeHTTP(result, request)
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"status":"revoked"`) {
		t.Fatalf("revoked status response = %d %s", result.Code, result.Body.String())
	}
}
