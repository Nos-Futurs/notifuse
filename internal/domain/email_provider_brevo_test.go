package domain_test

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrevoSettingsEncryptDecryptAndValidate(t *testing.T) {
	const passphrase = "test-passphrase"
	settings := domain.BrevoSettings{APIKey: "xkeysib-test-key"}
	require.NoError(t, settings.Validate(passphrase))
	require.NotEmpty(t, settings.EncryptedAPIKey)

	decrypted, err := crypto.DecryptFromHexString(settings.EncryptedAPIKey, passphrase)
	require.NoError(t, err)
	assert.Equal(t, "xkeysib-test-key", decrypted)

	loaded := domain.BrevoSettings{EncryptedAPIKey: settings.EncryptedAPIKey}
	require.NoError(t, loaded.DecryptAPIKey(passphrase))
	assert.Equal(t, "xkeysib-test-key", loaded.APIKey)

	err = (&domain.BrevoSettings{}).Validate(passphrase)
	assert.EqualError(t, err, "Brevo API key is required")
}

func TestEmailProviderWithBrevo(t *testing.T) {
	provider := domain.EmailProvider{
		Kind:               domain.EmailProviderKindBrevo,
		Brevo:              &domain.BrevoSettings{APIKey: "xkeysib-test-key"},
		Senders:            []domain.EmailSender{{ID: "sender", Email: "sender@example.com", Name: "Sender", IsDefault: true}},
		RateLimitPerMinute: 25,
	}
	require.NoError(t, provider.Validate("passphrase"))
	assert.False(t, provider.Kind.IsTransactionalOnly())
}
