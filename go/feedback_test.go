package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFeedbackLifecycleAndPersistence(t *testing.T) {
	dir := t.TempDir()
	store, err := newFeedbackStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 2, 1, 2, 3, 0, time.UTC)
	store.now = func() time.Time { return now }
	first, err := store.create("用户甲", "device-a", "希望增加一个说明", false)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	second, err := store.create("用户乙", "device-b", "移动端很好用", true)
	if err != nil {
		t.Fatal(err)
	}
	entries, total := store.list(1, 1)
	if total != 2 || len(entries) != 1 || entries[0].ID != second.ID {
		t.Fatalf("first page = %+v, total %d", entries, total)
	}
	if !entries[0].Anonymous || entries[0].Author != "用户乙" {
		t.Fatalf("anonymous entry persistence fields = %+v", entries[0])
	}
	now = now.Add(time.Minute)
	replied, err := store.reply(first.ID, "已收到，会评估。")
	if err != nil {
		t.Fatal(err)
	}
	if replied.Reply != "已收到，会评估。" || replied.RepliedAt == nil || !replied.RepliedAt.Equal(now) {
		t.Fatalf("replied entry = %+v", replied)
	}

	reloaded, err := newFeedbackStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries, total = reloaded.list(1, 10)
	if total != 2 || len(entries) != 2 || entries[1].Reply != "已收到，会评估。" {
		t.Fatalf("reloaded entries = %+v, total %d", entries, total)
	}
	if err := reloaded.delete(second.ID); err != nil {
		t.Fatal(err)
	}
	entries, total = reloaded.list(1, 10)
	if total != 1 || len(entries) != 1 || entries[0].ID != first.ID {
		t.Fatalf("entries after delete = %+v, total %d", entries, total)
	}
	if _, err := os.Stat(filepath.Join(dir, "feedback.json.bak")); err != nil {
		t.Fatalf("feedback backup missing: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "feedback.json")); err != nil {
		t.Fatal(err)
	}
	recovered, err := newFeedbackStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if entries, total := recovered.list(1, 10); total != 2 || len(entries) != 2 {
		t.Fatalf("backup recovery entries = %+v, total %d", entries, total)
	}
}

func TestFeedbackValidationRateLimitAndCorruptStore(t *testing.T) {
	store, err := newFeedbackStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 2, 2, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if _, err := store.create("用户", "device", " ", false); err == nil {
		t.Fatal("blank feedback was accepted")
	}
	if _, err := store.create("用户", "device", strings.Repeat("建", feedbackContentLimit+1), false); err == nil {
		t.Fatal("oversized feedback was accepted")
	}
	for index := 0; index < feedbackRateLimit; index++ {
		if _, err := store.create("用户", "device", "建议", false); err != nil {
			t.Fatalf("feedback %d error = %v", index+1, err)
		}
	}
	if _, err := store.create("用户", "device", "再发一条", false); !errors.Is(err, errFeedbackRateLimited) {
		t.Fatalf("rate limit error = %v", err)
	}
	now = now.Add(10*time.Minute + time.Second)
	if _, err := store.create("用户", "device", "限流结束", false); err != nil {
		t.Fatalf("feedback after rate window error = %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "feedback.json"), []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newFeedbackStore(dir); err == nil {
		t.Fatal("corrupt feedback store did not fail")
	}
}

func TestFeedbackConcurrentCreate(t *testing.T) {
	store, err := newFeedbackStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const count = 20
	errorsCh := make(chan error, count)
	var wait sync.WaitGroup
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, err := store.create("并发用户", fmt.Sprintf("device-%d", index), "并发留言", false)
			errorsCh <- err
		}(index)
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if entries, total := store.list(1, count); total != count || len(entries) != count {
		t.Fatalf("concurrent entries = %d, total %d", len(entries), total)
	}
}

func TestFeedbackHTTPPermissionsIdentityReplyAndDelete(t *testing.T) {
	ac := newTestAccessControl(t)
	store, err := newFeedbackStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	deviceToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	deviceHash := tokenHash(deviceToken)
	ac.mu.Lock()
	if err := ac.addAllowEntryLocked("device", deviceHash, "建议人甲", "", time.Now().UTC()); err != nil {
		ac.mu.Unlock()
		t.Fatal(err)
	}
	ac.mu.Unlock()
	mux := newServiceMux(ac, store)

	unauthorized := httptest.NewRequest(http.MethodGet, "http://report.example/api/feedback", nil)
	unauthorized.RemoteAddr = "127.0.0.1:50000"
	unauthorized.Header.Set("X-Forwarded-For", "203.0.113.20")
	unauthorizedResult := httptest.NewRecorder()
	mux.ServeHTTP(unauthorizedResult, unauthorized)
	if unauthorizedResult.Code != http.StatusForbidden {
		t.Fatalf("unauthorized feedback list = %d, want 403", unauthorizedResult.Code)
	}

	createBody := strings.NewReader(`{"content":"请增加留言功能"}`)
	create := httptest.NewRequest(http.MethodPost, "http://report.example/api/feedback", createBody)
	create.RemoteAddr = "127.0.0.1:50000"
	create.Header.Set("X-Forwarded-For", "203.0.113.20")
	create.Header.Set("Origin", "http://report.example")
	create.Header.Set("Content-Type", "application/json")
	create.AddCookie(&http.Cookie{Name: deviceCookieName, Value: deviceToken})
	createResult := httptest.NewRecorder()
	mux.ServeHTTP(createResult, create)
	if createResult.Code != http.StatusCreated {
		t.Fatalf("create feedback = %d: %s", createResult.Code, createResult.Body.String())
	}
	var created struct {
		Feedback feedbackView `json:"feedback"`
	}
	if err := json.Unmarshal(createResult.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Feedback.Author != "建议人甲" || created.Feedback.Content != "请增加留言功能" {
		t.Fatalf("created feedback = %+v", created.Feedback)
	}
	if strings.Contains(createResult.Body.String(), "author_key") {
		t.Fatal("public response exposed author_key")
	}

	anonymousBody := strings.NewReader(`{"content":"匿名建议","anonymous":true}`)
	anonymousCreate := httptest.NewRequest(http.MethodPost, "http://report.example/api/feedback", anonymousBody)
	anonymousCreate.RemoteAddr = "127.0.0.1:50000"
	anonymousCreate.Header.Set("X-Forwarded-For", "203.0.113.20")
	anonymousCreate.Header.Set("Origin", "http://report.example")
	anonymousCreate.Header.Set("Content-Type", "application/json")
	anonymousCreate.AddCookie(&http.Cookie{Name: deviceCookieName, Value: deviceToken})
	anonymousResult := httptest.NewRecorder()
	mux.ServeHTTP(anonymousResult, anonymousCreate)
	if anonymousResult.Code != http.StatusCreated {
		t.Fatalf("create anonymous feedback = %d: %s", anonymousResult.Code, anonymousResult.Body.String())
	}
	var anonymousCreated struct {
		Feedback feedbackView `json:"feedback"`
	}
	if err := json.Unmarshal(anonymousResult.Body.Bytes(), &anonymousCreated); err != nil {
		t.Fatal(err)
	}
	if anonymousCreated.Feedback.Author != "匿名用户" || !anonymousCreated.Feedback.Anonymous {
		t.Fatalf("public anonymous feedback = %+v", anonymousCreated.Feedback)
	}
	if strings.Contains(anonymousResult.Body.String(), "建议人甲") {
		t.Fatal("anonymous create response exposed real author")
	}

	publicList := httptest.NewRequest(http.MethodGet, "http://report.example/api/feedback", nil)
	publicList.RemoteAddr = "127.0.0.1:50000"
	publicList.Header.Set("X-Forwarded-For", "203.0.113.20")
	publicList.AddCookie(&http.Cookie{Name: deviceCookieName, Value: deviceToken})
	publicListResult := httptest.NewRecorder()
	mux.ServeHTTP(publicListResult, publicList)
	var publicFeedback struct {
		Feedback []feedbackView `json:"feedback"`
	}
	if err := json.Unmarshal(publicListResult.Body.Bytes(), &publicFeedback); err != nil {
		t.Fatal(err)
	}
	if publicListResult.Code != http.StatusOK || len(publicFeedback.Feedback) != 2 || publicFeedback.Feedback[0].Author != "匿名用户" || !publicFeedback.Feedback[0].Anonymous {
		t.Fatalf("public feedback list exposed anonymous author: %d %+v", publicListResult.Code, publicFeedback.Feedback)
	}

	adminToken := "admin-session"
	ac.mu.Lock()
	ac.sessions[tokenHash(adminToken)] = time.Now().Add(time.Hour)
	ac.mu.Unlock()
	adminList := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/admin/feedback", nil)
	adminList.RemoteAddr = "127.0.0.1:50000"
	adminList.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminToken})
	adminListResult := httptest.NewRecorder()
	mux.ServeHTTP(adminListResult, adminList)
	var adminFeedback struct {
		Feedback []feedbackView `json:"feedback"`
	}
	if err := json.Unmarshal(adminListResult.Body.Bytes(), &adminFeedback); err != nil {
		t.Fatal(err)
	}
	if adminListResult.Code != http.StatusOK || len(adminFeedback.Feedback) != 2 || adminFeedback.Feedback[0].Author != "建议人甲" || !adminFeedback.Feedback[0].Anonymous {
		t.Fatalf("admin feedback list did not reveal anonymous author: %d %+v", adminListResult.Code, adminFeedback.Feedback)
	}
	reply := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/admin/feedback/"+created.Feedback.ID+"/reply", strings.NewReader(`{"reply":"谢谢，已经上线。"}`))
	reply.RemoteAddr = "127.0.0.1:50000"
	reply.Header.Set("Origin", "http://127.0.0.1")
	reply.Header.Set("Content-Type", "application/json")
	reply.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminToken})
	replyResult := httptest.NewRecorder()
	mux.ServeHTTP(replyResult, reply)
	if replyResult.Code != http.StatusOK {
		t.Fatalf("reply feedback = %d: %s", replyResult.Code, replyResult.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "http://127.0.0.1/api/admin/feedback/"+created.Feedback.ID, nil)
	deleteRequest.RemoteAddr = "127.0.0.1:50000"
	deleteRequest.Header.Set("Origin", "http://127.0.0.1")
	deleteRequest.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminToken})
	deleteResult := httptest.NewRecorder()
	mux.ServeHTTP(deleteResult, deleteRequest)
	if deleteResult.Code != http.StatusOK {
		t.Fatalf("delete feedback = %d: %s", deleteResult.Code, deleteResult.Body.String())
	}
	anonymousDelete := httptest.NewRequest(http.MethodDelete, "http://127.0.0.1/api/admin/feedback/"+anonymousCreated.Feedback.ID, nil)
	anonymousDelete.RemoteAddr = "127.0.0.1:50000"
	anonymousDelete.Header.Set("Origin", "http://127.0.0.1")
	anonymousDelete.AddCookie(&http.Cookie{Name: adminCookieName, Value: adminToken})
	anonymousDeleteResult := httptest.NewRecorder()
	mux.ServeHTTP(anonymousDeleteResult, anonymousDelete)
	if anonymousDeleteResult.Code != http.StatusOK {
		t.Fatalf("delete anonymous feedback = %d: %s", anonymousDeleteResult.Code, anonymousDeleteResult.Body.String())
	}
	if _, total := store.list(1, 20); total != 0 {
		t.Fatalf("feedback total after delete = %d", total)
	}
}
