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
	serviceMux := newServiceMux(ac)

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
	newServiceMux(ac).ServeHTTP(httpsResult, httpsRequest)
	if cookie := httpsResult.Header().Get("Set-Cookie"); !strings.Contains(cookie, "Secure") {
		t.Fatalf("HTTPS device cookie missing Secure: %q", cookie)
	}

	httpRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/access/status", nil)
	httpRequest.RemoteAddr = "127.0.0.1:50000"
	httpResult := httptest.NewRecorder()
	newServiceMux(ac).ServeHTTP(httpResult, httpRequest)
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
	newServiceMux(ac).ServeHTTP(result, request)
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"status":"revoked"`) {
		t.Fatalf("revoked status response = %d %s", result.Code, result.Body.String())
	}
}
