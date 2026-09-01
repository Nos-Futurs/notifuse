package emailerror

import (
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestClassifierBrevo(t *testing.T) {
	classifier := NewClassifier()

	quota := classifier.Classify(errors.New(`Brevo API returned status code 402: {"code":"not_enough_credits"}`), domain.EmailProviderKindBrevo)
	assert.Equal(t, ErrorTypeProvider, quota.Type)
	assert.True(t, quota.Retryable)
	assert.Equal(t, 402, quota.HTTPStatus)

	invalid := classifier.Classify(errors.New(`Brevo API returned status code 400: invalid email`), domain.EmailProviderKindBrevo)
	assert.Equal(t, ErrorTypeRecipient, invalid.Type)
	assert.False(t, invalid.Retryable)
}
