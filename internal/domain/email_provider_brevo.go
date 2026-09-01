package domain

import (
	"fmt"

	"github.com/Notifuse/notifuse/pkg/crypto"
)

// BrevoSettings contains configuration for the Brevo v3 email API.
type BrevoSettings struct {
	EncryptedAPIKey string `json:"encrypted_api_key,omitempty"`

	// APIKey is decrypted at runtime and is never stored in plaintext.
	APIKey string `json:"api_key,omitempty"`
}

func (s *BrevoSettings) DecryptAPIKey(passphrase string) error {
	apiKey, err := crypto.DecryptFromHexString(s.EncryptedAPIKey, passphrase)
	if err != nil {
		return fmt.Errorf("failed to decrypt Brevo API key: %w", err)
	}
	s.APIKey = apiKey
	return nil
}

func (s *BrevoSettings) EncryptAPIKey(passphrase string) error {
	encryptedAPIKey, err := crypto.EncryptString(s.APIKey, passphrase)
	if err != nil {
		return fmt.Errorf("failed to encrypt Brevo API key: %w", err)
	}
	s.EncryptedAPIKey = encryptedAPIKey
	return nil
}

func (s *BrevoSettings) Validate(passphrase string) error {
	if s.APIKey == "" && s.EncryptedAPIKey == "" {
		return fmt.Errorf("Brevo API key is required")
	}
	if s.APIKey != "" {
		if err := s.EncryptAPIKey(passphrase); err != nil {
			return fmt.Errorf("failed to encrypt Brevo API key: %w", err)
		}
	}
	return nil
}

// BrevoWebhook describes a webhook returned by GET /v3/webhooks.
type BrevoWebhook struct {
	ID          int64    `json:"id"`
	URL         string   `json:"url"`
	Description string   `json:"description,omitempty"`
	Events      []string `json:"events"`
	Type        string   `json:"type"`
	Batched     bool     `json:"batched,omitempty"`
}

type BrevoWebhookListResponse struct {
	Webhooks []BrevoWebhook `json:"webhooks"`
}

// BrevoWebhookEvent represents a transactional email event delivered by Brevo.
type BrevoWebhookEvent struct {
	Event         string   `json:"event"`
	Email         string   `json:"email"`
	WebhookID     int64    `json:"id"`
	Date          string   `json:"date"`
	Timestamp     int64    `json:"ts"`
	EventTime     int64    `json:"ts_event"`
	Epoch         int64    `json:"ts_epoch"`
	MessageID     string   `json:"message-id"`
	Subject       string   `json:"subject,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	XMailinCustom string   `json:"X-Mailin-custom,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}
