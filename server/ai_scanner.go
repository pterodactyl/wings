package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/environment"
	"github.com/pterodactyl/wings/system"
)

// AiRule represents a cached dangerous startup command rule.
type AiRule struct {
	Command string `json:"command"`
	Level   int    `json:"level"`
	Reason  string `json:"reason"`
}

// AiBan represents a server UUID that has been banned by the AI scanner.
type AiBan struct {
	Uuid     string    `json:"uuid"`
	Reason   string    `json:"reason"`
	BannedAt time.Time `json:"banned_at"`
}

// AiScanner analyzes server startup commands using OpenRouter and bans servers
// that are classified as dangerous.
type AiScanner struct {
	cfg        config.AiScannerConfig
	httpClient *http.Client

	mu          sync.RWMutex
	rules       map[string]AiRule
	rulesLoaded bool
	bans        map[string]AiBan
	bansLoaded  bool
}

// NewAiScanner creates a new AI scanner instance.
func NewAiScanner(cfg config.AiScannerConfig) *AiScanner {
	return &AiScanner{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: time.Second * 30,
		},
		rules: make(map[string]AiRule),
		bans:  make(map[string]AiBan),
	}
}

// IsEnabled returns true if the scanner is configured and active.
func (as *AiScanner) IsEnabled() bool {
	return as.cfg.Enabled && as.cfg.IsValid()
}

// IsBanned returns true if the given server UUID is currently banned. The bans
// file is re-read on each call so manual unbans take effect immediately.
func (as *AiScanner) IsBanned(uuid string) bool {
	as.mu.Lock()
	defer as.mu.Unlock()

	if err := as.loadBansLocked(); err != nil {
		log.WithError(err).WithField("uuid", uuid).Warn("ai scanner: failed to load bans")
	}

	_, ok := as.bans[uuid]
	return ok
}

// Ban adds the server UUID to the bans file.
func (as *AiScanner) Ban(uuid, reason string) error {
	as.mu.Lock()
	defer as.mu.Unlock()

	if err := as.loadBansLocked(); err != nil {
		return errors.Wrap(err, "ai scanner: failed to load bans before banning")
	}

	as.bans[uuid] = AiBan{
		Uuid:     uuid,
		Reason:   reason,
		BannedAt: time.Now(),
	}

	return as.saveBansLocked()
}

// Unban removes the server UUID from the bans file.
func (as *AiScanner) Unban(uuid string) error {
	as.mu.Lock()
	defer as.mu.Unlock()

	if err := as.loadBansLocked(); err != nil {
		return errors.Wrap(err, "ai scanner: failed to load bans before unbanning")
	}

	delete(as.bans, uuid)
	return as.saveBansLocked()
}

// ScheduleCheck waits for the configured hang time and then analyzes the
// server's startup command if the server is still running.
func (as *AiScanner) ScheduleCheck(s *Server, command string) {
	if !as.IsEnabled() {
		return
	}

	time.Sleep(time.Duration(as.cfg.StartupHangSeconds) * time.Second)

	state := s.Environment.State()
	if state != environment.ProcessRunningState && state != environment.ProcessStartingState {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	level, reason, err := as.Analyze(ctx, command)
	if err != nil {
		log.WithError(err).WithField("server", s.ID()).Warn("ai scanner: failed to analyze startup command")
		return
	}

	log.WithFields(log.Fields{
		"server":  s.ID(),
		"command": command,
		"level":   level,
		"reason":  reason,
	}).Debug("ai scanner: analyzed startup command")

	if level < as.cfg.DangerThreshold {
		return
	}

	banReason := fmt.Sprintf("AI danger level %d/10: %s", level, reason)
	log.WithFields(log.Fields{
		"server": s.ID(),
		"level":  level,
		"reason": reason,
	}).Warn("ai scanner: dangerous startup command detected, banning server")

	if err := as.Ban(s.ID(), banReason); err != nil {
		log.WithError(err).WithField("server", s.ID()).Error("ai scanner: failed to ban server")
	}

	if err := s.Environment.Terminate(s.Context(), "SIGKILL"); err != nil {
		log.WithError(err).WithField("server", s.ID()).Error("ai scanner: failed to terminate server")
	}

	tgCfg := config.Get().System.TelegramNotifications.AsSystemConfig()
	msg := fmt.Sprintf(
		"🚨 *AI Scanner Alert*\nServer: `%s`\nDanger: %d/10\nReason: %s\nCommand: `%s`",
		s.ID(), level, reason, command,
	)
	system.SendTelegramNotificationAsync(tgCfg, msg)
}

// Analyze sends the startup command to OpenRouter and returns the danger level
// and reason. Dangerous commands are cached in the rules file.
func (as *AiScanner) Analyze(ctx context.Context, command string) (int, string, error) {
	as.mu.Lock()
	if !as.rulesLoaded {
		if err := as.loadRulesLocked(); err != nil {
			log.WithError(err).Warn("ai scanner: failed to load rules")
		}
	}
	if rule, ok := as.rules[command]; ok {
		as.mu.Unlock()
		return rule.Level, rule.Reason, nil
	}
	as.mu.Unlock()

	payload := map[string]interface{}{
		"model": as.cfg.OpenRouterModel,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You analyze Linux/Pterodactyl game server startup commands. Reply with valid JSON only: {\"danger\": 0-10, \"reason\": \"short explanation\"}. 0 is harmless, 10 is extremely dangerous (cryptomining, DDoS, malware, reverse shells, botnets, etc.).",
			},
			{
				"role":    "user",
				"content": command,
			},
		},
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return 0, "", errors.Wrap(err, "ai scanner: failed to marshal openrouter request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(b))
	if err != nil {
		return 0, "", errors.Wrap(err, "ai scanner: failed to create openrouter request")
	}
	req.Header.Set("Authorization", "Bearer "+as.cfg.OpenRouterApiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := as.httpClient.Do(req)
	if err != nil {
		return 0, "", errors.Wrap(err, "ai scanner: failed to contact openrouter")
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 0, "", errors.Errorf("ai scanner: openrouter returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, "", errors.Wrap(err, "ai scanner: failed to decode openrouter response")
	}
	if len(result.Choices) == 0 {
		return 0, "", errors.New("ai scanner: openrouter returned no choices")
	}

	content := result.Choices[0].Message.Content
	level, reason := as.parseAiResponse(content)

	if level >= as.cfg.DangerThreshold {
		as.mu.Lock()
		as.rules[command] = AiRule{
			Command: command,
			Level:   level,
			Reason:  reason,
		}
		if err := as.saveRulesLocked(); err != nil {
			log.WithError(err).Warn("ai scanner: failed to save rules")
		}
		as.mu.Unlock()
	}

	return level, reason, nil
}

// parseAiResponse extracts the danger level and reason from the assistant
// content. It first tries strict JSON, then falls back to regex.
func (as *AiScanner) parseAiResponse(content string) (int, string) {
	var parsed struct {
		Danger int    `json:"danger"`
		Reason string `json:"reason"`
	}

	if err := json.Unmarshal([]byte(content), &parsed); err == nil {
		return as.normalizeResponse(parsed.Danger, parsed.Reason)
	}

	// Fallback: try to extract from markdown code block or raw text.
	reCode := regexp.MustCompile("```(?:json)?\\s*([\\s\\S]*?)```")
	if m := reCode.FindStringSubmatch(content); len(m) > 1 {
		if err := json.Unmarshal([]byte(m[1]), &parsed); err == nil {
			return as.normalizeResponse(parsed.Danger, parsed.Reason)
		}
	}

	reDanger := regexp.MustCompile(`"danger"\s*[:=]\s*(\d+)`)
	if m := reDanger.FindStringSubmatch(content); len(m) > 1 {
		parsed.Danger, _ = strconv.Atoi(m[1])
	}

	reReason := regexp.MustCompile(`"reason"\s*[:=]\s*"([^"]+)"`)
	if m := reReason.FindStringSubmatch(content); len(m) > 1 {
		parsed.Reason = m[1]
	}

	return as.normalizeResponse(parsed.Danger, parsed.Reason)
}

func (as *AiScanner) normalizeResponse(level int, reason string) (int, string) {
	if level < 0 {
		level = 0
	}
	if level > 10 {
		level = 10
	}
	if reason == "" {
		reason = "no reason provided"
	}
	return level, reason
}

// loadRulesLocked reads the rules cache from disk.
func (as *AiScanner) loadRulesLocked() error {
	if as.rulesLoaded {
		return nil
	}

	data, err := os.ReadFile(as.cfg.RulesPath)
	if err != nil {
		if os.IsNotExist(err) {
			as.rulesLoaded = true
			return nil
		}
		return err
	}

	var rules []AiRule
	if err := json.Unmarshal(data, &rules); err != nil {
		return err
	}

	for _, r := range rules {
		as.rules[r.Command] = r
	}
	as.rulesLoaded = true
	return nil
}

// saveRulesLocked writes the rules cache to disk.
func (as *AiScanner) saveRulesLocked() error {
	if err := os.MkdirAll(filepath.Dir(as.cfg.RulesPath), 0o755); err != nil {
		return err
	}

	var rules []AiRule
	for _, r := range as.rules {
		rules = append(rules, r)
	}
	sort.Slice(rules, func(i, j int) bool {
		return rules[i].Command < rules[j].Command
	})

	data, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(as.cfg.RulesPath, data, 0o644)
}

// loadBansLocked reads the bans file from disk.
func (as *AiScanner) loadBansLocked() error {
	data, err := os.ReadFile(as.cfg.BansPath)
	if err != nil {
		if os.IsNotExist(err) {
			as.bans = make(map[string]AiBan)
			as.bansLoaded = true
			return nil
		}
		return err
	}

	var bans []AiBan
	if err := json.Unmarshal(data, &bans); err != nil {
		return err
	}

	fresh := make(map[string]AiBan, len(bans))
	for _, b := range bans {
		fresh[b.Uuid] = b
	}
	as.bans = fresh
	as.bansLoaded = true
	return nil
}

// saveBansLocked writes the bans file to disk.
func (as *AiScanner) saveBansLocked() error {
	if err := os.MkdirAll(filepath.Dir(as.cfg.BansPath), 0o755); err != nil {
		return err
	}

	var bans []AiBan
	for _, b := range as.bans {
		bans = append(bans, b)
	}
	sort.Slice(bans, func(i, j int) bool {
		return bans[i].Uuid < bans[j].Uuid
	})

	data, err := json.MarshalIndent(bans, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(as.cfg.BansPath, data, 0o644)
}
