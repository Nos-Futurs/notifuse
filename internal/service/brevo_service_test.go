package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/service"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func brevoResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewBufferString(body))}
}

func TestBrevoServiceSendEmail(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := mocks.NewMockHTTPClient(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	svc := service.NewBrevoService(httpClient, log)
	captured := ""
	request := domain.SendEmailProviderRequest{
		WorkspaceID: "workspace", IntegrationID: "integration", MessageID: "notifuse-message",
		FromAddress: "sender@example.com", FromName: "Sender", To: "recipient@example.com",
		Subject: "Subject", Content: "<p>Hello</p>", CapturedMessageID: &captured,
		Provider: &domain.EmailProvider{Kind: domain.EmailProviderKindBrevo, Brevo: &domain.BrevoSettings{APIKey: "xkeysib-test"}},
		EmailOptions: domain.EmailOptions{
			CC: []string{"cc@example.com"}, BCC: []string{"bcc@example.com"}, ReplyTo: "reply@example.com",
			ListUnsubscribeURL: "https://example.com/unsubscribe",
		},
	}

	httpClient.EXPECT().Do(gomock.Any()).DoAndReturn(func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodPost, req.Method)
		assert.Equal(t, "https://api.brevo.com/v3/smtp/email", req.URL.String())
		assert.Equal(t, "xkeysib-test", req.Header.Get("api-key"))
		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
		assert.Equal(t, "<p>Hello</p>", body["htmlContent"])
		headers := body["headers"].(map[string]interface{})
		assert.Equal(t, "notifuse_message_id:notifuse-message", headers["X-Mailin-custom"])
		assert.Equal(t, "<https://example.com/unsubscribe>", headers["List-Unsubscribe"])
		assert.NotNil(t, body["cc"])
		assert.NotNil(t, body["bcc"])
		assert.NotNil(t, body["replyTo"])
		return brevoResponse(http.StatusCreated, `{"messageId":"provider-message-id"}`), nil
	})

	require.NoError(t, svc.SendEmail(context.Background(), request))
	assert.Equal(t, "provider-message-id", captured)
}

func TestBrevoServiceRegisterWebhooks(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := mocks.NewMockHTTPClient(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	svc := service.NewBrevoService(httpClient, log)
	provider := &domain.EmailProvider{Kind: domain.EmailProviderKindBrevo, Brevo: &domain.BrevoSettings{APIKey: "xkeysib-test"}}

	gomock.InOrder(
		httpClient.EXPECT().Do(gomock.Any()).DoAndReturn(func(req *http.Request) (*http.Response, error) {
			assert.Equal(t, http.MethodGet, req.Method)
			assert.Equal(t, "transactional", req.URL.Query().Get("type"))
			return brevoResponse(http.StatusOK, `{"webhooks":[]}`), nil
		}),
		httpClient.EXPECT().Do(gomock.Any()).DoAndReturn(func(req *http.Request) (*http.Response, error) {
			assert.Equal(t, http.MethodPost, req.Method)
			var body struct {
				URL    string   `json:"url"`
				Events []string `json:"events"`
			}
			require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
			assert.Contains(t, body.URL, "provider=brevo")
			assert.ElementsMatch(t, []string{"blocked", "delivered", "hardBounce", "invalid", "softBounce", "spam"}, body.Events)
			return brevoResponse(http.StatusCreated, `{"id":42}`), nil
		}),
	)

	status, err := svc.RegisterWebhooks(context.Background(), "workspace", "integration", "https://notifuse.example", []domain.EmailEventType{
		domain.EmailEventDelivered, domain.EmailEventBounce, domain.EmailEventComplaint,
	}, provider)
	require.NoError(t, err)
	assert.True(t, status.IsRegistered)
	assert.Equal(t, domain.EmailProviderKindBrevo, status.EmailProviderKind)
	require.Len(t, status.Endpoints, 3)
	assert.Equal(t, "42", status.Endpoints[0].WebhookID)
}

func TestBrevoServiceRegisterWebhooksTreatsDocumentNotFoundAsEmptyList(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := mocks.NewMockHTTPClient(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	svc := service.NewBrevoService(httpClient, log)
	provider := &domain.EmailProvider{Kind: domain.EmailProviderKindBrevo, Brevo: &domain.BrevoSettings{APIKey: "xkeysib-test"}}

	gomock.InOrder(
		httpClient.EXPECT().Do(gomock.Any()).DoAndReturn(func(req *http.Request) (*http.Response, error) {
			assert.Equal(t, http.MethodGet, req.Method)
			return brevoResponse(http.StatusBadRequest, `{"code":"document_not_found","message":"Webhook record does not exist"}`), nil
		}),
		httpClient.EXPECT().Do(gomock.Any()).DoAndReturn(func(req *http.Request) (*http.Response, error) {
			assert.Equal(t, http.MethodPost, req.Method)
			return brevoResponse(http.StatusCreated, `{"id":43}`), nil
		}),
	)

	status, err := svc.RegisterWebhooks(
		context.Background(),
		"workspace",
		"integration",
		"https://notifuse.example",
		[]domain.EmailEventType{
			domain.EmailEventDelivered,
			domain.EmailEventBounce,
			domain.EmailEventComplaint,
		},
		provider,
	)
	require.NoError(t, err)
	assert.True(t, status.IsRegistered)
	assert.Equal(t, "43", status.Endpoints[0].WebhookID)
}

func TestBrevoServicePreservesAPIErrorBody(t *testing.T) {
	ctrl := gomock.NewController(t)
	httpClient := mocks.NewMockHTTPClient(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	svc := service.NewBrevoService(httpClient, log)
	httpClient.EXPECT().Do(gomock.Any()).Return(brevoResponse(http.StatusPaymentRequired, `{"code":"not_enough_credits","message":"Not enough credits"}`), nil)
	err := svc.SendEmail(context.Background(), domain.SendEmailProviderRequest{
		WorkspaceID: "workspace", IntegrationID: "integration", MessageID: "message",
		FromAddress: "sender@example.com", FromName: "Sender", To: "recipient@example.com", Subject: "Subject", Content: "Body",
		Provider: &domain.EmailProvider{Kind: domain.EmailProviderKindBrevo, Brevo: &domain.BrevoSettings{APIKey: "xkeysib-test"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status code 402")
	assert.Contains(t, err.Error(), "not_enough_credits")
}
