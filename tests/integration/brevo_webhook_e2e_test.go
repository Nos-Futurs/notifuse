package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/app"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrevoWebhookE2E(t *testing.T) {
	testutil.SkipIfShort(t)
	testutil.SetupTestEnvironment()
	defer testutil.CleanupTestEnvironment()
	suite := testutil.NewIntegrationTestSuite(t, func(cfg *config.Config) testutil.AppInterface { return app.NewApp(cfg) })
	defer suite.Cleanup()
	factory := suite.DataFactory
	workspace, err := factory.CreateWorkspace()
	require.NoError(t, err)
	integration, err := factory.CreateIntegration(workspace.ID, testutil.WithIntegrationEmailProvider(domain.EmailProvider{
		Kind: domain.EmailProviderKindBrevo, Brevo: &domain.BrevoSettings{APIKey: "test-only"},
	}))
	require.NoError(t, err)
	db, err := suite.DBManager.GetWorkspaceDB(workspace.ID)
	require.NoError(t, err)
	template, err := factory.CreateTemplate(workspace.ID)
	require.NoError(t, err)
	endpoint := fmt.Sprintf("/webhooks/email?provider=brevo&workspace_id=%s&integration_id=%s", workspace.ID, integration.ID)
	application := suite.ServerManager.GetApp()

	send := func(t *testing.T, payload domain.BrevoWebhookEvent, status int) {
		t.Helper()
		body, err := json.Marshal(payload)
		require.NoError(t, err)
		resp, err := suite.APIClient.PostRaw(endpoint, string(body))
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, status, resp.StatusCode)
		if status == http.StatusTooManyRequests {
			assert.Equal(t, "60", resp.Header.Get("Retry-After"))
		}
	}

	for _, eventName := range []string{"delivered", "hard_bounce", "invalid_email", "spam", "soft_bounce", "blocked"} {
		t.Run(eventName, func(t *testing.T) {
			contact, err := factory.CreateContact(workspace.ID)
			require.NoError(t, err)
			list, err := factory.CreateList(workspace.ID)
			require.NoError(t, err)
			_, err = factory.CreateContactList(workspace.ID, testutil.WithContactListEmail(contact.Email), testutil.WithContactListListID(list.ID), testutil.WithContactListStatus(domain.ContactListStatusActive))
			require.NoError(t, err)
			newMessage := func() *domain.MessageHistory {
				message, err := factory.CreateMessageHistory(workspace.ID, testutil.WithMessageHistoryContactEmail(contact.Email), testutil.WithMessageTemplate(template.ID), func(m *domain.MessageHistory) { m.ListID = &list.ID })
				require.NoError(t, err)
				return message
			}
			message := newMessage()
			payload := domain.BrevoWebhookEvent{Event: eventName, Email: contact.Email, MessageID: "<provider-" + message.ID + ">", XMailinCustom: "notifuse_message_id:" + message.ID, EventTime: time.Now().Unix(), Reason: "test diagnostic", WebhookID: 42}
			send(t, payload, http.StatusOK)
			// Retry through a different subscription and with a later callback time.
			payload.WebhookID = 99
			payload.EventTime++
			for i := 0; i < 5; i++ {
				send(t, payload, http.StatusOK)
			}
			var count int
			require.NoError(t, db.QueryRowContext(context.Background(), "SELECT count(*) FROM inbound_webhook_events WHERE recipient_email=$1", contact.Email).Scan(&count))
			assert.Equal(t, 1, count, "replays must not inflate bounce counts")
			updated := fetchMessageByID(t, application, workspace.ID, message.ID)
			member, err := application.GetContactListRepository().GetContactListByIDs(context.Background(), workspace.ID, contact.Email, list.ID)
			require.NoError(t, err)
			switch eventName {
			case "delivered":
				assert.NotNil(t, updated.DeliveredAt)
				assert.Equal(t, domain.ContactListStatusActive, member.Status)
			case "hard_bounce", "invalid_email":
				assert.NotNil(t, updated.BouncedAt)
				assert.Equal(t, domain.ContactListStatusBounced, member.Status)
			case "spam":
				assert.NotNil(t, updated.ComplainedAt)
				assert.Equal(t, domain.ContactListStatusComplained, member.Status)
			case "soft_bounce", "blocked":
				assert.NotNil(t, updated.FailedAt)
				require.NotNil(t, updated.StatusInfo)
				assert.Contains(t, *updated.StatusInfo, eventName)
				assert.Nil(t, updated.BouncedAt)
				assert.Equal(t, domain.ContactListStatusActive, member.Status)
				for i := 0; i < 4; i++ {
					next := newMessage()
					payload.MessageID = "provider-" + next.ID
					payload.XMailinCustom = "notifuse_message_id:" + next.ID
					send(t, payload, http.StatusOK)
				}
				member, err = application.GetContactListRepository().GetContactListByIDs(context.Background(), workspace.ID, contact.Email, list.ID)
				require.NoError(t, err)
				assert.Equal(t, domain.ContactListStatusBounced, member.Status, "distinct failed messages reach suppression threshold")
			}
		})
	}

	t.Run("retry recovers failure after event storage", func(t *testing.T) {
		contact, err := factory.CreateContact(workspace.ID)
		require.NoError(t, err)
		message, err := factory.CreateMessageHistory(workspace.ID, testutil.WithMessageHistoryContactEmail(contact.Email), testutil.WithMessageTemplate(template.ID))
		require.NoError(t, err)
		_, err = db.Exec(`CREATE FUNCTION brevo_test_fail_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'simulated temporary failure'; END $$;
CREATE TRIGGER brevo_test_fail_update BEFORE UPDATE ON message_history FOR EACH ROW EXECUTE FUNCTION brevo_test_fail_update()`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = db.Exec(`DROP TRIGGER IF EXISTS brevo_test_fail_update ON message_history; DROP FUNCTION IF EXISTS brevo_test_fail_update()`)
		})
		payload := domain.BrevoWebhookEvent{Event: "delivered", Email: contact.Email, MessageID: "provider-" + message.ID, XMailinCustom: "notifuse_message_id:" + message.ID, EventTime: time.Now().Unix()}
		send(t, payload, http.StatusTooManyRequests)
		_, err = db.Exec(`DROP TRIGGER brevo_test_fail_update ON message_history; DROP FUNCTION brevo_test_fail_update()`)
		require.NoError(t, err)
		send(t, payload, http.StatusOK)
		assert.NotNil(t, fetchMessageByID(t, application, workspace.ID, message.ID).DeliveredAt)
		var count int
		require.NoError(t, db.QueryRow("SELECT count(*) FROM inbound_webhook_events WHERE recipient_email=$1", contact.Email).Scan(&count))
		assert.Equal(t, 1, count)
	})
}
