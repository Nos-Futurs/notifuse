package service

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessBrevoWebhook(t *testing.T) {
	svc := &InboundWebhookEventService{}
	events, err := svc.processBrevoWebhook("integration", []byte(`[
		{"event":"delivered","email":"one@example.com","ts_event":1700000000,"message-id":"provider-1","X-Mailin-custom":"notifuse_message_id:notifuse-1"},
		{"event":"hard_bounce","email":"two@example.com","ts_event":1700000001,"message-id":"provider-2","reason":"mailbox does not exist"},
		{"event":"spam","email":"three@example.com","ts_event":1700000002,"message-id":"provider-3"},
		{"event":"opened","email":"ignored@example.com","ts_event":1700000003,"message-id":"provider-4"}
	]`))
	require.NoError(t, err)
	require.Len(t, events, 3)
	assert.Equal(t, domain.WebhookSourceBrevo, events[0].Source)
	assert.Equal(t, domain.EmailEventDelivered, events[0].Type)
	assert.Equal(t, "notifuse-1", *events[0].MessageID)
	assert.Equal(t, domain.EmailEventBounce, events[1].Type)
	assert.Equal(t, "hard_bounce", events[1].BounceType)
	assert.Equal(t, "mailbox does not exist", events[1].BounceDiagnostic)
	assert.Equal(t, domain.EmailEventComplaint, events[2].Type)
	assert.Equal(t, "spam", events[2].ComplaintFeedbackType)
}

func TestBrevoNotifuseMessageID(t *testing.T) {
	assert.Equal(t, "abc", brevoNotifuseMessageID("other:value|notifuse_message_id:abc"))
	assert.Empty(t, brevoNotifuseMessageID("other:value"))
}

func TestBrevoCallbackIdentity(t *testing.T) {
	expected := domain.GenerateWebhookCallbackURL("https://notifuse.example", domain.EmailProviderKindBrevo, "workspace", "integration")
	for _, tc := range []struct {
		url   string
		match bool
	}{
		{"https://notifuse.example/webhooks/email?workspace_id=workspace&provider=brevo&integration_id=integration", true},
		{"https://notifuse.example/webhooks/email?workspace_id=workspace&provider=brevo&integration_id=integration&custom=value", true},
		{"https://other.example/webhooks/email?workspace_id=workspace&provider=brevo&integration_id=integration", false},
		{"https://notifuse.example/other/webhooks/email?workspace_id=workspace&provider=brevo&integration_id=integration", false},
		{"https://notifuse.example/webhooks/email?workspace_id=workspace&provider=brevo&integration_id=other", false},
		{"https://notifuse.example/webhooks/email?workspace_id=workspace&provider=brevo&integration_id=integration&integration_id=other", false},
	} {
		assert.Equal(t, tc.match, brevoCallbackMatches(tc.url, expected), tc.url)
	}
}
