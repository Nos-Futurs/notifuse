package service

import (
	"context"
	"encoding/json"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"strings"
	"testing"
	"time"

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

func TestBrevoTerminalEventMappings(t *testing.T) {
	for _, tc := range []struct {
		event          string
		kind           domain.EmailEventType
		bounce         string
		classification domain.BounceClassification
	}{
		{"delivered", domain.EmailEventDelivered, "", 0},
		{"hard_bounce", domain.EmailEventBounce, "hard_bounce", domain.BounceClassificationHard},
		{"soft_bounce", domain.EmailEventBounce, "soft_bounce", domain.BounceClassificationSoftCount},
		{"blocked", domain.EmailEventBounce, "blocked", domain.BounceClassificationSoftCount},
		{"invalid", domain.EmailEventBounce, "invalid", domain.BounceClassificationHard},
		{"spam", domain.EmailEventComplaint, "", 0},
	} {
		t.Run(tc.event, func(t *testing.T) {
			body, err := json.Marshal(domain.BrevoWebhookEvent{Event: tc.event, Email: "recipient@example.com", EventTime: 1700000000, MessageID: "provider-id", XMailinCustom: "notifuse_message_id:internal-id", Reason: "diagnostic"})
			require.NoError(t, err)
			events, err := (&InboundWebhookEventService{}).processBrevoWebhook("integration", body)
			require.NoError(t, err)
			require.Len(t, events, 1)
			event := events[0]
			assert.Equal(t, tc.kind, event.Type)
			assert.Equal(t, "internal-id", *event.MessageID)
			assert.Equal(t, time.Unix(1700000000, 0).UTC(), event.Timestamp)
			assert.Equal(t, tc.bounce, event.BounceType)
			if tc.kind == domain.EmailEventBounce {
				assert.Equal(t, "diagnostic", event.BounceDiagnostic)
				assert.Equal(t, tc.classification, domain.ClassifyBounce(domain.BounceInput{Provider: domain.EmailProviderKindBrevo, Type: event.BounceType, Subtype: event.BounceCategory}))
			}
		})
	}
}

func TestBrevoWebhookRejectsMalformedJSON(t *testing.T) {
	_, err := (&InboundWebhookEventService{}).processBrevoWebhook("integration", []byte(`{"event":`))
	require.Error(t, err)
}

func TestBrevoWebhookPipeline(t *testing.T) {
	for _, tc := range []struct {
		name         string
		event        string
		messageEvent domain.MessageEvent
		softCount    int
		suppress     bool
	}{
		{"delivered", "delivered", domain.MessageEventDelivered, 0, false},
		{"hard bounce", "hard_bounce", domain.MessageEventBounced, 0, true},
		{"invalid", "invalid", domain.MessageEventBounced, 0, true},
		{"spam", "spam", domain.MessageEventComplained, 0, false},
		{"soft bounce below threshold", "soft_bounce", domain.MessageEventFailed, 1, false},
		{"soft bounce at threshold", "soft_bounce", domain.MessageEventFailed, 5, true},
		{"blocked below threshold", "blocked", domain.MessageEventFailed, 1, false},
		{"blocked at threshold", "blocked", domain.MessageEventFailed, 5, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mocks.NewMockInboundWebhookEventRepository(ctrl)
			workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
			history := mocks.NewMockMessageHistoryRepository(ctrl)
			contacts := mocks.NewMockContactRepository(ctrl)
			log := pkgmocks.NewMockLogger(ctrl)
			log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
			log.EXPECT().Debug(gomock.Any()).AnyTimes()
			workspaceRepo.EXPECT().GetByID(gomock.Any(), "workspace").Return(&domain.Workspace{ID: "workspace", Integrations: []domain.Integration{{ID: "integration", EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindBrevo}}}}, nil)
			repo.EXPECT().StoreEvents(gomock.Any(), "workspace", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, events []*domain.InboundWebhookEvent) error {
				require.Len(t, events, 1)
				assert.Equal(t, domain.WebhookSourceBrevo, events[0].Source)
				assert.Equal(t, "recipient@example.com", events[0].RecipientEmail)
				return nil
			})
			history.EXPECT().SetStatusesIfNotSet(gomock.Any(), "workspace", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, updates []domain.MessageEventUpdate) error {
				if tc.messageEvent == "" {
					require.Empty(t, updates)
				} else {
					require.Len(t, updates, 1)
					assert.Equal(t, "internal-id", updates[0].ID)
					assert.Equal(t, tc.messageEvent, updates[0].Event)
				}
				return nil
			})
			if tc.softCount > 0 {
				repo.EXPECT().CountConsecutiveSoftBounces(gomock.Any(), "workspace", []string{"recipient@example.com"}).Return(map[string]int{"recipient@example.com": tc.softCount}, nil)
			}
			if tc.suppress {
				contacts.EXPECT().MarkEmailsAsBounced(gomock.Any(), "workspace", []string{"recipient@example.com"}, gomock.Any()).Return(nil)
			}
			body, err := json.Marshal(domain.BrevoWebhookEvent{Event: tc.event, Email: "recipient@example.com", EventTime: 1700000000, XMailinCustom: "notifuse_message_id:internal-id"})
			require.NoError(t, err)
			svc := &InboundWebhookEventService{repo: repo, workspaceRepo: workspaceRepo, messageHistoryRepo: history, contactRepo: contacts, logger: log}
			require.NoError(t, svc.ProcessWebhook(context.Background(), "workspace", "integration", body))
		})
	}
}

func TestBrevoWebhookReplayIdentity(t *testing.T) {
	svc := &InboundWebhookEventService{}
	original := `{"event":"soft_bounce","email":"recipient@example.com","ts_event":1700000000,"message-id":"<provider-1>","id":42,"X-Mailin-custom":"notifuse_message_id:message-1"}`
	first, err := svc.processBrevoWebhook("integration", []byte(original))
	require.NoError(t, err)
	retry := `{"event":"soft_bounce","email":"recipient@example.com","ts_event":1700000001,"message-id":"provider-1","id":99,"reason":"updated diagnostic","X-Mailin-custom":"notifuse_message_id:message-1"}`
	repeated, err := svc.processBrevoWebhook("integration", []byte("["+retry+"]"))
	require.NoError(t, err)
	assert.Equal(t, first[0].ID, repeated[0].ID, "subscription, batch format, retry time and diagnostics do not create a new bounce")
	for _, tc := range []struct{ name, integration, payload string }{
		{"different integration", "other", original},
		{"different message", "integration", strings.ReplaceAll(original, "provider-1", "provider-2")},
		{"different recipient", "integration", strings.ReplaceAll(original, "recipient@example.com", "other@example.com")},
		{"different event", "integration", strings.ReplaceAll(original, "soft_bounce", "delivered")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events, err := svc.processBrevoWebhook(tc.integration, []byte(tc.payload))
			require.NoError(t, err)
			assert.NotEqual(t, first[0].ID, events[0].ID)
		})
	}
}

func TestBrevoWebhookInvalidEmailAndMissingCorrelation(t *testing.T) {
	events, err := (&InboundWebhookEventService{}).processBrevoWebhook("integration", []byte(`{"event":"invalid_email","email":"invalid-address","ts_event":1700000000,"message-id":"external-id"}`))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "invalid", events[0].BounceType)
	assert.Equal(t, domain.EmailEventBounce, events[0].Type)
	assert.Nil(t, events[0].MessageID, "provider IDs must not be used as internal primary keys")
}

func TestBrevoWebhookValidation(t *testing.T) {
	for _, payload := range []string{
		`null`, `{}`, `[null]`, `42`, `{"event":"delivered"}`,
		`{"event":"delivered","email":"x@example.com","message-id":"id"}`,
		`{"event":"delivered","email":"x@example.com","ts_event":1700000000}`,
		`[{"event":"delivered","email":"x@example.com","message-id":"id","ts_event":1700000000},{}]`,
	} {
		t.Run(payload, func(t *testing.T) {
			events, err := (&InboundWebhookEventService{}).processBrevoWebhook("integration", []byte(payload))
			require.ErrorIs(t, err, domain.ErrInvalidWebhookPayload)
			assert.Empty(t, events, "reject the whole malformed batch before storing anything")
		})
	}
	for _, payload := range []string{`[]`, `{"event":"deferred"}`, `{"event":"opened"}`} {
		events, err := (&InboundWebhookEventService{}).processBrevoWebhook("integration", []byte(payload))
		require.NoError(t, err)
		assert.Empty(t, events)
	}
}

func TestBrevoWebhookTimestampFallback(t *testing.T) {
	for _, field := range []string{`"ts_event":1700000000`, `"ts":1700000000`, `"ts_epoch":1700000000000`} {
		events, err := (&InboundWebhookEventService{}).processBrevoWebhook("integration", []byte(`{"event":"delivered","email":"x@example.com","message-id":"id",`+field+`}`))
		require.NoError(t, err)
		assert.Equal(t, time.Unix(1700000000, 0).UTC(), events[0].Timestamp)
	}
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
