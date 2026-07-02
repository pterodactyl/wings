package system

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
)

// TelegramConfig holds the parameters needed to send a Telegram notification.
type TelegramConfig struct {
	BotToken string
	ChatId   string
	ThreadId int
}

// IsValid returns true if the configuration has the minimum required fields.
func (c TelegramConfig) IsValid() bool {
	return c.BotToken != "" && c.ChatId != ""
}

// SendTelegramNotification sends a message to the configured Telegram chat.
// It is a no-op if Telegram notifications are not enabled or misconfigured.
func SendTelegramNotification(cfg TelegramConfig, message string) error {
	if !cfg.IsValid() {
		return nil
	}

	payload := map[string]interface{}{
		"chat_id":    cfg.ChatId,
		"text":       message,
		"parse_mode": "Markdown",
	}
	if cfg.ThreadId != 0 {
		payload["message_thread_id"] = cfg.ThreadId
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return errors.Wrap(err, "telegram: failed to marshal request")
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)
	client := &http.Client{Timeout: time.Second * 15}
	resp, err := client.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return errors.Wrap(err, "telegram: failed to send message")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return errors.Errorf("telegram: returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// SendTelegramNotificationAsync sends a Telegram notification in a goroutine.
// Errors are logged but not returned.
func SendTelegramNotificationAsync(cfg TelegramConfig, message string) {
	if !cfg.IsValid() {
		return
	}

	go func() {
		if err := SendTelegramNotification(cfg, message); err != nil {
			log.WithError(err).Warn("telegram: failed to send notification")
		}
	}()
}
