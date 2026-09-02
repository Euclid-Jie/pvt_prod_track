package main

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type feedbackView struct {
	ID        string     `json:"id"`
	Author    string     `json:"author"`
	Anonymous bool       `json:"anonymous,omitempty"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"created_at"`
	Reply     string     `json:"reply,omitempty"`
	RepliedAt *time.Time `json:"replied_at,omitempty"`
}

type feedbackHTTP struct {
	access *accessControl
	store  *feedbackStore
}

func feedbackToView(entry feedbackEntry, revealAnonymousAuthor bool) feedbackView {
	author := entry.Author
	if entry.Anonymous && !revealAnonymousAuthor {
		author = "匿名用户"
	}
	return feedbackView{
		ID:        entry.ID,
		Author:    author,
		Anonymous: entry.Anonymous,
		Content:   entry.Content,
		CreatedAt: entry.CreatedAt,
		Reply:     entry.Reply,
		RepliedAt: entry.RepliedAt,
	}
}

func feedbackPagination(r *http.Request) (int, int, error) {
	page, pageSize := 1, 20
	var err error
	if value := r.URL.Query().Get("page"); value != "" {
		page, err = strconv.Atoi(value)
		if err != nil || page < 1 {
			return 0, 0, errors.New("页码无效")
		}
	}
	if value := r.URL.Query().Get("page_size"); value != "" {
		pageSize, err = strconv.Atoi(value)
		if err != nil || pageSize < 1 || pageSize > 100 {
			return 0, 0, errors.New("每页数量必须在 1 到 100 之间")
		}
	}
	return page, pageSize, nil
}

func (handler *feedbackHTTP) handleList(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := feedbackPagination(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	entries, total := handler.store.list(page, pageSize)
	views := make([]feedbackView, 0, len(entries))
	revealAnonymousAuthor := strings.HasPrefix(r.URL.Path, "/api/admin/")
	for _, entry := range entries {
		views = append(views, feedbackToView(entry, revealAnonymousAuthor))
	}
	writeJSON(w, map[string]any{
		"feedback":  views,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (handler *feedbackHTTP) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeAPIError(w, http.StatusForbidden, "origin_mismatch", "请求来源无效")
		return
	}
	_, deviceHash, err := handler.access.ensureDevice(w, r)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "device_error", "无法识别留言设备")
		return
	}
	author := handler.access.viewerName(deviceHash, clientIP(r))
	authorKey := deviceHash
	if handler.access.sessionValid(adminSessionToken(r)) {
		author = "管理员"
		authorKey = "admin"
	}
	var input struct {
		Content   string `json:"content"`
		Anonymous bool   `json:"anonymous"`
	}
	if !decodeJSONBody(w, r, &input, 8192) {
		return
	}
	entry, err := handler.store.create(author, authorKey, input.Content, input.Anonymous)
	if errors.Is(err, errFeedbackRateLimited) {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "留言过于频繁，请十分钟后再试")
		return
	}
	if err != nil {
		if _, validationErr := validateFeedbackContent(input.Content); validationErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_feedback", validationErr.Error())
			return
		}
		log.Printf("保存留言失败: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "feedback_save_failed", "留言保存失败，请稍后重试")
		return
	}
	writeJSONStatus(w, http.StatusCreated, map[string]any{"feedback": feedbackToView(entry, false)})
}

func (handler *feedbackHTTP) handleReply(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Reply string `json:"reply"`
	}
	if !decodeJSONBody(w, r, &input, 8192) {
		return
	}
	entry, err := handler.store.reply(r.PathValue("id"), input.Reply)
	if errors.Is(err, errFeedbackNotFound) {
		writeAPIError(w, http.StatusNotFound, "feedback_not_found", "留言不存在")
		return
	}
	if err != nil {
		if _, validationErr := validateFeedbackReply(input.Reply); validationErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_reply", validationErr.Error())
			return
		}
		log.Printf("保存留言回复失败: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "feedback_reply_failed", "回复保存失败，请稍后重试")
		return
	}
	writeJSON(w, map[string]any{"feedback": feedbackToView(entry, true)})
}

func (handler *feedbackHTTP) handleDelete(w http.ResponseWriter, r *http.Request) {
	err := handler.store.delete(r.PathValue("id"))
	if errors.Is(err, errFeedbackNotFound) {
		writeAPIError(w, http.StatusNotFound, "feedback_not_found", "留言不存在")
		return
	}
	if err != nil {
		log.Printf("删除留言失败: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "feedback_delete_failed", "删除失败，请稍后重试")
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}
