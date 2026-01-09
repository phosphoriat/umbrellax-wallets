package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"wallets/internal/config"

	"gopkg.in/telebot.v4"
)

// Client Notification HTTP client struct
type Client struct {
	httpClient        *http.Client
	MessengerApiUrl   string
	MessengerBotId    string
	MessengerBotToken string
	botClient         *telebot.Bot
	telegramGroupId   int64
}

// Global package vars
var (
	clientInstance *Client
	once           sync.Once
)

// GetClient Get global notification Client instance
func GetClient() *Client {
	once.Do(func() {
		clientInstance = newNotificationClient()
	})
	return clientInstance
}

// @private newNotificationClient Create new Client instance (private constructor)
func newNotificationClient() *Client {
	// Load config
	cfg, _ := config.Load()

	// Init Telebot
	botClient, err := telebot.NewBot(telebot.Settings{
		Token:   cfg.TelegramBotToken,
		Offline: true,
	})
	if err != nil {
		log.Fatalf("[Notification API] Telegram bot client error: %v", err)
	}

	return &Client{
		httpClient:        &http.Client{Timeout: 5 * time.Second},
		MessengerApiUrl:   cfg.MessengerApiUrl,
		MessengerBotId:    cfg.MessengerBotId,
		MessengerBotToken: cfg.MessengerBotToken,
		botClient:         botClient,
		telegramGroupId:   cfg.TelegramGroupId,
	}
}

// SendNotifications Send notifications to channels
func (c *Client) SendNotifications(ctx context.Context, message string) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	wg.Add(2)
	go func() {
		defer wg.Done()

		if err := c.sendTelegramNotification(message); err != nil {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()

		if err := c.sendMessengerNotification(ctx, message); err != nil {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}
	}()

	wg.Wait()
	return errors.Join(errs...)
}

// @private sendTelegramNotification
// message: Error message
func (c *Client) sendTelegramNotification(message string) error {
	if c.telegramGroupId != 0 {
		_, err := c.botClient.Send(telebot.ChatID(c.telegramGroupId), message)
		return err
	}

	return nil
}

// @private sendMessengerNotification
// ctx: Current context
// message: Error message
func (c *Client) sendMessengerNotification(ctx context.Context, message string) error {
	if c.MessengerApiUrl != "" {
		// Create request payload
		data, err := json.Marshal(map[string]any{
			"bot_id":    c.MessengerBotId,
			"bot_token": c.MessengerBotToken,
			"text":      message,
		})
		if err != nil {
			return fmt.Errorf("[Messenger API] JSON payload serialize error: %w", err)
		}
		payload := bytes.NewReader(data)

		// Create new request entity
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.MessengerApiUrl, payload)
		if err != nil {
			return fmt.Errorf("[Messenger API] Failed to create request: %w", err)
		}

		// Add headers
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")

		// Execute request and get response
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("[Messenger API] Failed to execute request: %w", err)
		}
		defer resp.Body.Close()

		// Check response status
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("[Messenger API] Response failed with status %d: %s", resp.StatusCode, body)
		}
	}

	return nil
}
