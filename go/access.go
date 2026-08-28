package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	accessStateVersion   = 1
	applicationRateLimit = 5
	loginFailureLimit    = 5
)

type accessApplication struct {
	ID         string     `json:"id"`
	DeviceHash string     `json:"device_hash"`
	IP         string     `json:"ip"`
	Name       string     `json:"name"`
	Contact    string     `json:"contact,omitempty"`
	Message    string     `json:"message"`
	Status     string     `json:"status"`
	Decision   string     `json:"decision,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	DecidedAt  *time.Time `json:"decided_at,omitempty"`
}

type allowEntry struct {
	ID                  string     `json:"id"`
	Kind                string     `json:"kind"`
	Value               string     `json:"value"`
	Label               string     `json:"label"`
	SourceApplicationID string     `json:"source_application_id,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	RevokedAt           *time.Time `json:"revoked_at,omitempty"`
}

type accessState struct {
	Version           int                 `json:"version"`
	AdminPasswordHash string              `json:"admin_password_hash,omitempty"`
	Applications      []accessApplication `json:"applications"`
	Allowlist         []allowEntry        `json:"allowlist"`
}

type accessControl struct {
	mu             sync.RWMutex
	path           string
	state          accessState
	setupTokenHash string
	sessions       map[string]time.Time
	loginFailures  map[string][]time.Time
	now            func() time.Time
}

type applicationInput struct {
	Name    string `json:"name"`
	Contact string `json:"contact"`
	Message string `json:"message"`
}

var errRateLimited = errors.New("rate limited")
var errPendingApplication = errors.New("pending application already exists")

func newAccessControl(dir string, resetAdmin bool) (*accessControl, error) {
	ac := &accessControl{
		path:          filepath.Join(dir, "access_control.json"),
		sessions:      make(map[string]time.Time),
		loginFailures: make(map[string][]time.Time),
		now:           time.Now,
		state: accessState{
			Version:      accessStateVersion,
			Applications: []accessApplication{},
			Allowlist:    []allowEntry{},
		},
	}
	if err := ac.load(); err != nil {
		return nil, err
	}
	if resetAdmin {
		ac.state.AdminPasswordHash = ""
		if err := ac.saveLocked(); err != nil {
			return nil, err
		}
	}
	if ac.state.AdminPasswordHash == "" {
		token, err := randomToken(24)
		if err != nil {
			return nil, err
		}
		ac.setupTokenHash = tokenHash(token)
		log.Printf("首次设置管理员口令: %s", token)
		log.Printf("请通过本机监听端口打开 /admin/setup 完成设置")
	}
	return ac, nil
}

func (ac *accessControl) load() error {
	data, err := os.ReadFile(ac.path)
	if errors.Is(err, os.ErrNotExist) {
		backupPath := ac.path + ".bak"
		data, err = os.ReadFile(backupPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("读取访问控制配置失败: %w", err)
	}
	var state accessState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("解析访问控制配置失败: %w", err)
	}
	if state.Version != accessStateVersion {
		return fmt.Errorf("不支持的访问控制配置版本: %d", state.Version)
	}
	if state.Applications == nil {
		state.Applications = []accessApplication{}
	}
	if state.Allowlist == nil {
		state.Allowlist = []allowEntry{}
	}
	ac.state = state
	return nil
}

// saveLocked persists the complete state. The caller must hold ac.mu for writes.
func (ac *accessControl) saveLocked() error {
	data, err := json.MarshalIndent(ac.state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(ac.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".access_control-*.tmp")
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

	backupPath := ac.path + ".bak"
	_ = os.Remove(backupPath)
	if _, err := os.Stat(ac.path); err == nil {
		if err := os.Rename(ac.path, backupPath); err != nil {
			_ = os.Remove(tmpPath)
			return err
		}
	}
	if err := os.Rename(tmpPath, ac.path); err != nil {
		_ = os.Rename(backupPath, ac.path)
		_ = os.Remove(tmpPath)
		return err
	}
	_ = os.Remove(backupPath)
	return nil
}

func randomToken(byteCount int) (string, error) {
	buf := make([]byte, byteCount)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func validDeviceToken(token string) bool {
	data, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(data) == 32
}

func shortDeviceID(deviceHash string) string {
	if len(deviceHash) <= 12 {
		return strings.ToUpper(deviceHash)
	}
	return strings.ToUpper(deviceHash[:12])
}

func canonicalIP(value string) string {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return ""
	}
	return ip.String()
}

func (ac *accessControl) configured() bool {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return ac.state.AdminPasswordHash != ""
}

func (ac *accessControl) isAllowed(deviceHash, ip string) bool {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	for _, entry := range ac.state.Allowlist {
		if entry.RevokedAt != nil {
			continue
		}
		if entry.Kind == "device" && entry.Value == deviceHash {
			return true
		}
		if entry.Kind == "ip" && entry.Value == ip {
			return true
		}
	}
	return false
}

func validateApplicationInput(input applicationInput) (applicationInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Contact = strings.TrimSpace(input.Contact)
	input.Message = strings.TrimSpace(input.Message)
	if input.Name == "" || input.Message == "" {
		return input, errors.New("称呼和留言不能为空")
	}
	if utf8.RuneCountInString(input.Name) > 80 {
		return input, errors.New("称呼不能超过 80 个字符")
	}
	if utf8.RuneCountInString(input.Contact) > 160 {
		return input, errors.New("联系方式不能超过 160 个字符")
	}
	if utf8.RuneCountInString(input.Message) > 1000 {
		return input, errors.New("留言不能超过 1000 个字符")
	}
	return input, nil
}

func (ac *accessControl) createApplication(deviceHash, ip string, input applicationInput) (accessApplication, error) {
	input, err := validateApplicationInput(input)
	if err != nil {
		return accessApplication{}, err
	}
	if deviceHash == "" || canonicalIP(ip) == "" {
		return accessApplication{}, errors.New("无法识别设备或 IP")
	}
	now := ac.now().UTC()
	ac.mu.Lock()
	defer ac.mu.Unlock()
	for i := len(ac.state.Applications) - 1; i >= 0; i-- {
		application := ac.state.Applications[i]
		if application.DeviceHash == deviceHash && application.Status == "pending" {
			return application, errPendingApplication
		}
	}
	cutoff := now.Add(-time.Hour)
	count := 0
	for _, application := range ac.state.Applications {
		if application.CreatedAt.After(cutoff) && (application.DeviceHash == deviceHash || application.IP == ip) {
			count++
		}
	}
	if count >= applicationRateLimit {
		return accessApplication{}, errRateLimited
	}
	id, err := randomToken(12)
	if err != nil {
		return accessApplication{}, err
	}
	application := accessApplication{
		ID:         "req_" + id,
		DeviceHash: deviceHash,
		IP:         ip,
		Name:       input.Name,
		Contact:    input.Contact,
		Message:    input.Message,
		Status:     "pending",
		CreatedAt:  now,
	}
	ac.state.Applications = append(ac.state.Applications, application)
	if err := ac.saveLocked(); err != nil {
		ac.state.Applications = ac.state.Applications[:len(ac.state.Applications)-1]
		return accessApplication{}, err
	}
	return application, nil
}

func (ac *accessControl) latestApplication(deviceHash string) *accessApplication {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	for i := len(ac.state.Applications) - 1; i >= 0; i-- {
		if ac.state.Applications[i].DeviceHash == deviceHash {
			application := ac.state.Applications[i]
			return &application
		}
	}
	return nil
}

func (ac *accessControl) listApplications(status string) []accessApplication {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	applications := make([]accessApplication, 0, len(ac.state.Applications))
	for _, application := range ac.state.Applications {
		if status == "" || status == "all" || application.Status == status {
			applications = append(applications, application)
		}
	}
	sort.Slice(applications, func(i, j int) bool {
		return applications[i].CreatedAt.After(applications[j].CreatedAt)
	})
	return applications
}

func (ac *accessControl) listAllowlist() []allowEntry {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	entries := append([]allowEntry(nil), ac.state.Allowlist...)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CreatedAt.After(entries[j].CreatedAt)
	})
	return entries
}

func (ac *accessControl) approveApplication(id, grant string) (accessApplication, error) {
	if grant == "" {
		grant = "device"
	}
	if grant != "device" && grant != "ip" && grant != "both" {
		return accessApplication{}, errors.New("批准类型必须是 device、ip 或 both")
	}
	now := ac.now().UTC()
	ac.mu.Lock()
	defer ac.mu.Unlock()
	index := -1
	for i := range ac.state.Applications {
		if ac.state.Applications[i].ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		return accessApplication{}, os.ErrNotExist
	}
	if ac.state.Applications[index].Status != "pending" {
		return accessApplication{}, errors.New("该申请已处理")
	}
	previousAllowlist := append([]allowEntry(nil), ac.state.Allowlist...)
	previousApplication := ac.state.Applications[index]
	application := &ac.state.Applications[index]
	application.Status = "approved"
	application.Decision = grant
	application.DecidedAt = &now
	if grant == "device" || grant == "both" {
		if err := ac.addAllowEntryLocked("device", application.DeviceHash, application.Name, application.ID, now); err != nil {
			ac.state.Allowlist = previousAllowlist
			ac.state.Applications[index] = previousApplication
			return accessApplication{}, err
		}
	}
	if grant == "ip" || grant == "both" {
		if err := ac.addAllowEntryLocked("ip", application.IP, application.Name, application.ID, now); err != nil {
			ac.state.Allowlist = previousAllowlist
			ac.state.Applications[index] = previousApplication
			return accessApplication{}, err
		}
	}
	if err := ac.saveLocked(); err != nil {
		ac.state.Allowlist = previousAllowlist
		ac.state.Applications[index] = previousApplication
		return accessApplication{}, err
	}
	return *application, nil
}

func (ac *accessControl) addAllowEntryLocked(kind, value, label, sourceID string, now time.Time) error {
	for _, entry := range ac.state.Allowlist {
		if entry.Kind == kind && entry.Value == value && entry.RevokedAt == nil {
			return nil
		}
	}
	id, err := randomToken(12)
	if err != nil {
		return err
	}
	ac.state.Allowlist = append(ac.state.Allowlist, allowEntry{
		ID:                  "allow_" + id,
		Kind:                kind,
		Value:               value,
		Label:               label,
		SourceApplicationID: sourceID,
		CreatedAt:           now,
	})
	return nil
}

func (ac *accessControl) rejectApplication(id string) (accessApplication, error) {
	now := ac.now().UTC()
	ac.mu.Lock()
	defer ac.mu.Unlock()
	for i := range ac.state.Applications {
		application := &ac.state.Applications[i]
		if application.ID != id {
			continue
		}
		if application.Status != "pending" {
			return accessApplication{}, errors.New("该申请已处理")
		}
		previous := *application
		application.Status = "rejected"
		application.Decision = "rejected"
		application.DecidedAt = &now
		if err := ac.saveLocked(); err != nil {
			*application = previous
			return accessApplication{}, err
		}
		return *application, nil
	}
	return accessApplication{}, os.ErrNotExist
}

func (ac *accessControl) revokeAllowEntry(id string) (allowEntry, error) {
	now := ac.now().UTC()
	ac.mu.Lock()
	defer ac.mu.Unlock()
	for i := range ac.state.Allowlist {
		entry := &ac.state.Allowlist[i]
		if entry.ID != id {
			continue
		}
		if entry.RevokedAt != nil {
			return *entry, errors.New("该白名单已撤销")
		}
		entry.RevokedAt = &now
		if err := ac.saveLocked(); err != nil {
			entry.RevokedAt = nil
			return allowEntry{}, err
		}
		return *entry, nil
	}
	return allowEntry{}, os.ErrNotExist
}

func (ac *accessControl) deleteApplication(id string) error {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	for i := range ac.state.Applications {
		if ac.state.Applications[i].ID != id {
			continue
		}
		if ac.state.Applications[i].Status == "pending" {
			return errors.New("请先批准或拒绝该申请")
		}
		previous := append([]accessApplication(nil), ac.state.Applications...)
		ac.state.Applications = append(ac.state.Applications[:i], ac.state.Applications[i+1:]...)
		if err := ac.saveLocked(); err != nil {
			ac.state.Applications = previous
			return err
		}
		return nil
	}
	return os.ErrNotExist
}

func (ac *accessControl) setupAdmin(setupToken, password string) error {
	if len(password) < 12 || len(password) > 128 {
		return errors.New("管理密码长度必须为 12 至 128 个字符")
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if ac.state.AdminPasswordHash != "" {
		return errors.New("管理员密码已经设置")
	}
	if ac.setupTokenHash == "" || subtle.ConstantTimeCompare([]byte(tokenHash(setupToken)), []byte(ac.setupTokenHash)) != 1 {
		return errors.New("一次性设置口令无效")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	ac.state.AdminPasswordHash = string(hash)
	if err := ac.saveLocked(); err != nil {
		ac.state.AdminPasswordHash = ""
		return err
	}
	ac.setupTokenHash = ""
	return nil
}

func (ac *accessControl) login(ip, password string) (string, error) {
	now := ac.now()
	ac.mu.Lock()
	defer ac.mu.Unlock()
	failures := recentTimes(ac.loginFailures[ip], now.Add(-15*time.Minute))
	ac.loginFailures[ip] = failures
	if len(failures) >= loginFailureLimit {
		return "", errRateLimited
	}
	if ac.state.AdminPasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(ac.state.AdminPasswordHash), []byte(password)) != nil {
		ac.loginFailures[ip] = append(failures, now)
		return "", errors.New("管理密码错误")
	}
	delete(ac.loginFailures, ip)
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	ac.sessions[tokenHash(token)] = now.Add(8 * time.Hour)
	return token, nil
}

func recentTimes(values []time.Time, cutoff time.Time) []time.Time {
	kept := values[:0]
	for _, value := range values {
		if value.After(cutoff) {
			kept = append(kept, value)
		}
	}
	return kept
}

func (ac *accessControl) sessionValid(token string) bool {
	if token == "" {
		return false
	}
	now := ac.now()
	hash := tokenHash(token)
	ac.mu.Lock()
	defer ac.mu.Unlock()
	expires, ok := ac.sessions[hash]
	if !ok || !expires.After(now) {
		delete(ac.sessions, hash)
		return false
	}
	return true
}

func (ac *accessControl) logout(token string) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	delete(ac.sessions, tokenHash(token))
}

func (ac *accessControl) changePassword(currentPassword, newPassword string) error {
	if len(newPassword) < 12 || len(newPassword) > 128 {
		return errors.New("新密码长度必须为 12 至 128 个字符")
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if bcrypt.CompareHashAndPassword([]byte(ac.state.AdminPasswordHash), []byte(currentPassword)) != nil {
		return errors.New("当前密码错误")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	previous := ac.state.AdminPasswordHash
	ac.state.AdminPasswordHash = string(hash)
	if err := ac.saveLocked(); err != nil {
		ac.state.AdminPasswordHash = previous
		return err
	}
	ac.sessions = make(map[string]time.Time)
	return nil
}
