package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	feedbackStateVersion = 1
	feedbackRateLimit    = 5
	feedbackContentLimit = 500
	feedbackReplyLimit   = 1000
)

var (
	errFeedbackNotFound    = errors.New("feedback not found")
	errFeedbackRateLimited = errors.New("feedback rate limited")
)

type feedbackEntry struct {
	ID        string     `json:"id"`
	Author    string     `json:"author"`
	AuthorKey string     `json:"author_key"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"created_at"`
	Reply     string     `json:"reply,omitempty"`
	RepliedAt *time.Time `json:"replied_at,omitempty"`
}

type feedbackState struct {
	Version int             `json:"version"`
	Entries []feedbackEntry `json:"entries"`
}

type feedbackStore struct {
	mu    sync.RWMutex
	path  string
	state feedbackState
	now   func() time.Time
}

func newFeedbackStore(dir string) (*feedbackStore, error) {
	store := &feedbackStore{
		path: filepath.Join(dir, "feedback.json"),
		state: feedbackState{
			Version: feedbackStateVersion,
			Entries: []feedbackEntry{},
		},
		now: time.Now,
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (store *feedbackStore) load() error {
	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		data, err = os.ReadFile(store.path + ".bak")
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	if err != nil {
		return err
	}
	var state feedbackState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("解析留言数据失败: %w", err)
	}
	if state.Version != feedbackStateVersion {
		return fmt.Errorf("不支持的留言数据版本: %d", state.Version)
	}
	if state.Entries == nil {
		state.Entries = []feedbackEntry{}
	}
	store.state = state
	return nil
}

// saveLocked persists the complete state. The caller must hold store.mu for writes.
func (store *feedbackStore) saveLocked() error {
	data, err := json.MarshalIndent(store.state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(store.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".feedback-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0600); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	backupPath := store.path + ".bak"
	if _, err := os.Stat(store.path); err == nil {
		_ = os.Remove(backupPath)
		if err := os.Rename(store.path, backupPath); err != nil {
			_ = os.Remove(tmpPath)
			return err
		}
	}
	if err := os.Rename(tmpPath, store.path); err != nil {
		_ = os.Rename(backupPath, store.path)
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func validateFeedbackContent(content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errors.New("留言不能为空")
	}
	if utf8.RuneCountInString(content) > feedbackContentLimit {
		return "", fmt.Errorf("留言不能超过 %d 个字符", feedbackContentLimit)
	}
	return content, nil
}

func validateFeedbackReply(reply string) (string, error) {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return "", errors.New("回复不能为空")
	}
	if utf8.RuneCountInString(reply) > feedbackReplyLimit {
		return "", fmt.Errorf("回复不能超过 %d 个字符", feedbackReplyLimit)
	}
	return reply, nil
}

func (store *feedbackStore) create(author, authorKey, content string) (feedbackEntry, error) {
	content, err := validateFeedbackContent(content)
	if err != nil {
		return feedbackEntry{}, err
	}
	author = strings.TrimSpace(author)
	if author == "" || authorKey == "" {
		return feedbackEntry{}, errors.New("无法识别留言人")
	}
	author = truncateRunes(author, 80)
	now := store.now().UTC()

	store.mu.Lock()
	defer store.mu.Unlock()
	cutoff := now.Add(-10 * time.Minute)
	count := 0
	for _, entry := range store.state.Entries {
		if entry.AuthorKey == authorKey && entry.CreatedAt.After(cutoff) {
			count++
		}
	}
	if count >= feedbackRateLimit {
		return feedbackEntry{}, errFeedbackRateLimited
	}
	id, err := randomToken(12)
	if err != nil {
		return feedbackEntry{}, err
	}
	entry := feedbackEntry{
		ID:        "feedback_" + id,
		Author:    author,
		AuthorKey: authorKey,
		Content:   content,
		CreatedAt: now,
	}
	store.state.Entries = append(store.state.Entries, entry)
	if err := store.saveLocked(); err != nil {
		store.state.Entries = store.state.Entries[:len(store.state.Entries)-1]
		return feedbackEntry{}, err
	}
	return entry, nil
}

func (store *feedbackStore) list(page, pageSize int) ([]feedbackEntry, int) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	total := len(store.state.Entries)
	if total == 0 || page > (total-1)/pageSize+1 {
		return []feedbackEntry{}, total
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if end > total {
		end = total
	}
	entries := make([]feedbackEntry, 0, end-start)
	for position := start; position < end; position++ {
		entries = append(entries, store.state.Entries[total-1-position])
	}
	return entries, total
}

func (store *feedbackStore) reply(id, reply string) (feedbackEntry, error) {
	reply, err := validateFeedbackReply(reply)
	if err != nil {
		return feedbackEntry{}, err
	}
	now := store.now().UTC()
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.state.Entries {
		if store.state.Entries[index].ID != id {
			continue
		}
		previous := store.state.Entries[index]
		store.state.Entries[index].Reply = reply
		store.state.Entries[index].RepliedAt = &now
		if err := store.saveLocked(); err != nil {
			store.state.Entries[index] = previous
			return feedbackEntry{}, err
		}
		return store.state.Entries[index], nil
	}
	return feedbackEntry{}, errFeedbackNotFound
}

func (store *feedbackStore) delete(id string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.state.Entries {
		if store.state.Entries[index].ID != id {
			continue
		}
		previous := append([]feedbackEntry(nil), store.state.Entries...)
		store.state.Entries = append(store.state.Entries[:index:index], store.state.Entries[index+1:]...)
		if err := store.saveLocked(); err != nil {
			store.state.Entries = previous
			return err
		}
		return nil
	}
	return errFeedbackNotFound
}
