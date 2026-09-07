package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const brevoAPIBaseURL = "https://api.brevo.com"

// BrevoService sends email and manages transactional webhooks through Brevo's v3 API.
// Notifuse renders and sends both marketing and transactional messages itself, so both
// usages go through Brevo's transactional send endpoint and its transactional webhooks.
type BrevoService struct {
	httpClient domain.HTTPClient
	logger     logger.Logger
}

func NewBrevoService(httpClient domain.HTTPClient, logger logger.Logger) *BrevoService {
	return &BrevoService{httpClient: httpClient, logger: logger}
}

func (s *BrevoService) newRequest(ctx context.Context, method, endpoint, apiKey string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, brevoAPIBaseURL+endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("failed to create Brevo request: %w", err)
	}
	req.Header.Set("api-key", apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func brevoAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if len(body) == 0 {
		return fmt.Errorf("Brevo API returned status code %d", resp.StatusCode)
	}
	return fmt.Errorf("Brevo API returned status code %d: %s", resp.StatusCode, string(body))
}

func (s *BrevoService) listWebhooks(ctx context.Context, config domain.BrevoSettings) ([]domain.BrevoWebhook, error) {
	req, err := s.newRequest(ctx, http.MethodGet, "/v3/webhooks?type=transactional&sort=desc", config.APIKey, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list Brevo webhooks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// Fresh Brevo accounts can return document_not_found instead of an empty
		// collection until their first webhook exists. Treat that response as the
		// empty state so RegisterWebhooks can create the initial webhook.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if resp.StatusCode == http.StatusBadRequest {
			var apiError struct {
				Code string `json:"code"`
			}
			if json.Unmarshal(body, &apiError) == nil && apiError.Code == "document_not_found" {
				return []domain.BrevoWebhook{}, nil
			}
		}
		if len(body) > 0 {
			return nil, fmt.Errorf("Brevo API returned status code %d: %s", resp.StatusCode, string(body))
		}
		return nil, brevoAPIError(resp)
	}
	var result domain.BrevoWebhookListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode Brevo webhooks: %w", err)
	}
	return result.Webhooks, nil
}

func (s *BrevoService) createWebhook(ctx context.Context, config domain.BrevoSettings, webhookURL, description string, events []string) (int64, error) {
	payload := struct {
		URL         string   `json:"url"`
		Description string   `json:"description"`
		Events      []string `json:"events"`
		Type        string   `json:"type"`
		Batched     bool     `json:"batched"`
	}{
		URL: webhookURL, Description: description, Events: events, Type: "transactional", Batched: false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("failed to encode Brevo webhook: %w", err)
	}
	req, err := s.newRequest(ctx, http.MethodPost, "/v3/webhooks", config.APIKey, body)
	if err != nil {
		return 0, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to create Brevo webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		return 0, brevoAPIError(resp)
	}
	var result struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to decode Brevo webhook response: %w", err)
	}
	return result.ID, nil
}

func (s *BrevoService) deleteWebhook(ctx context.Context, config domain.BrevoSettings, webhookID int64) error {
	endpoint := "/v3/webhooks/" + url.PathEscape(strconv.FormatInt(webhookID, 10))
	req, err := s.newRequest(ctx, http.MethodDelete, endpoint, config.APIKey, nil)
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete Brevo webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return brevoAPIError(resp)
	}
	return nil
}

// updateWebhook preserves the existing subscription when an update fails.
// Deleting first would lose working delivery events during an API outage.
func (s *BrevoService) updateWebhook(ctx context.Context, config domain.BrevoSettings, webhookID int64, events []string) error {
	body, err := json.Marshal(struct {
		Events []string `json:"events"`
	}{Events: events})
	if err != nil {
		return err
	}
	req, err := s.newRequest(ctx, http.MethodPut, "/v3/webhooks/"+strconv.FormatInt(webhookID, 10), config.APIKey, body)
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to update Brevo webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return brevoAPIError(resp)
	}
	return nil
}

func brevoWebhookEvents(eventTypes []domain.EmailEventType) []string {
	set := map[string]struct{}{}
	for _, eventType := range eventTypes {
		switch eventType {
		case domain.EmailEventDelivered:
			set["delivered"] = struct{}{}
		case domain.EmailEventBounce:
			set["hardBounce"] = struct{}{}
			set["softBounce"] = struct{}{}
			set["blocked"] = struct{}{}
			set["invalid"] = struct{}{}
		case domain.EmailEventComplaint:
			set["spam"] = struct{}{}
		}
	}
	events := make([]string, 0, len(set))
	for event := range set {
		events = append(events, event)
	}
	sort.Strings(events)
	return events
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa, bb := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

// Match callback identity by URL components, allowing query reordering while
// refusing a different path, integration, workspace, or duplicate query value.
func brevoCallbackMatches(candidate, expected string) bool {
	actual, err := url.Parse(candidate)
	if err != nil || actual.Host == "" || (actual.Scheme != "https" && actual.Scheme != "http") || actual.User != nil || actual.Fragment != "" {
		return false
	}
	want, err := url.Parse(expected)
	if err != nil || actual.Path != want.Path {
		return false
	}
	if want.Host != "" && (actual.Host != want.Host || actual.Scheme != want.Scheme) {
		return false
	}
	query, err := url.ParseQuery(actual.RawQuery)
	if err != nil {
		return false
	}
	for key, values := range want.Query() {
		if !sameStrings(query[key], values) {
			return false
		}
	}
	return true
}

func brevoWebhookStatus(workspaceID, integrationID, webhookURL string, webhookID int64, eventTypes []domain.EmailEventType) *domain.WebhookRegistrationStatus {
	status := &domain.WebhookRegistrationStatus{
		EmailProviderKind: domain.EmailProviderKindBrevo,
		IsRegistered:      webhookID != 0,
		ProviderDetails: map[string]interface{}{
			"workspace_id": workspaceID, "integration_id": integrationID,
		},
	}
	for _, eventType := range eventTypes {
		status.Endpoints = append(status.Endpoints, domain.WebhookEndpointStatus{
			WebhookID: strconv.FormatInt(webhookID, 10), URL: webhookURL, EventType: eventType, Active: webhookID != 0,
		})
	}
	return status
}

func (s *BrevoService) RegisterWebhooks(ctx context.Context, workspaceID, integrationID, baseURL string, eventTypes []domain.EmailEventType, providerConfig *domain.EmailProvider) (*domain.WebhookRegistrationStatus, error) {
	if providerConfig == nil || providerConfig.Brevo == nil || providerConfig.Brevo.APIKey == "" {
		return nil, fmt.Errorf("Brevo configuration is missing or invalid")
	}
	webhookURL := domain.GenerateWebhookCallbackURL(baseURL, domain.EmailProviderKindBrevo, workspaceID, integrationID)
	desiredEvents := brevoWebhookEvents(eventTypes)
	if len(desiredEvents) == 0 {
		return nil, fmt.Errorf("no supported Brevo webhook events requested")
	}
	webhooks, err := s.listWebhooks(ctx, *providerConfig.Brevo)
	if err != nil {
		return nil, err
	}
	for _, webhook := range webhooks {
		if webhook.Type != "transactional" || !brevoCallbackMatches(webhook.URL, webhookURL) {
			continue
		}
		if sameStrings(webhook.Events, desiredEvents) {
			return brevoWebhookStatus(workspaceID, integrationID, webhookURL, webhook.ID, eventTypes), nil
		}
		if err := s.updateWebhook(ctx, *providerConfig.Brevo, webhook.ID, desiredEvents); err != nil {
			return nil, err
		}
		return brevoWebhookStatus(workspaceID, integrationID, webhookURL, webhook.ID, eventTypes), nil
	}
	webhookID, err := s.createWebhook(ctx, *providerConfig.Brevo, webhookURL, "Notifuse delivery events", desiredEvents)
	if err != nil {
		return nil, err
	}
	return brevoWebhookStatus(workspaceID, integrationID, webhookURL, webhookID, eventTypes), nil
}

func (s *BrevoService) GetWebhookStatus(ctx context.Context, workspaceID, integrationID string, providerConfig *domain.EmailProvider) (*domain.WebhookRegistrationStatus, error) {
	if providerConfig == nil || providerConfig.Brevo == nil || providerConfig.Brevo.APIKey == "" {
		return nil, fmt.Errorf("Brevo configuration is missing or invalid")
	}
	webhookURL := domain.GenerateWebhookCallbackURL("", domain.EmailProviderKindBrevo, workspaceID, integrationID)
	webhooks, err := s.listWebhooks(ctx, *providerConfig.Brevo)
	if err != nil {
		return nil, err
	}
	status := brevoWebhookStatus(workspaceID, integrationID, webhookURL, 0, nil)
	for _, webhook := range webhooks {
		if webhook.Type != "transactional" || !brevoCallbackMatches(webhook.URL, webhookURL) {
			continue
		}
		status.IsRegistered = true
		configured := make(map[string]bool, len(webhook.Events))
		for _, event := range webhook.Events {
			configured[event] = true
		}
		for _, eventType := range []domain.EmailEventType{
			domain.EmailEventDelivered, domain.EmailEventBounce, domain.EmailEventComplaint,
		} {
			active := true
			for _, required := range brevoWebhookEvents([]domain.EmailEventType{eventType}) {
				active = active && configured[required]
			}
			status.Endpoints = append(status.Endpoints, domain.WebhookEndpointStatus{
				WebhookID: strconv.FormatInt(webhook.ID, 10), URL: webhook.URL,
				EventType: eventType, Active: active,
			})
		}
	}
	return status, nil
}

func (s *BrevoService) UnregisterWebhooks(ctx context.Context, workspaceID, integrationID string, providerConfig *domain.EmailProvider) error {
	if providerConfig == nil || providerConfig.Brevo == nil || providerConfig.Brevo.APIKey == "" {
		return fmt.Errorf("Brevo configuration is missing or invalid")
	}
	webhookSuffix := domain.GenerateWebhookCallbackURL("", domain.EmailProviderKindBrevo, workspaceID, integrationID)
	webhooks, err := s.listWebhooks(ctx, *providerConfig.Brevo)
	if err != nil {
		return err
	}
	for _, webhook := range webhooks {
		if webhook.Type == "transactional" && brevoCallbackMatches(webhook.URL, webhookSuffix) {
			if err := s.deleteWebhook(ctx, *providerConfig.Brevo, webhook.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// SendEmail sends a fully rendered message using POST /v3/smtp/email.
func (s *BrevoService) SendEmail(ctx context.Context, request domain.SendEmailProviderRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	if request.Provider.Brevo == nil || request.Provider.Brevo.APIKey == "" {
		return fmt.Errorf("Brevo provider is not configured")
	}

	type address struct {
		Email string `json:"email"`
		Name  string `json:"name,omitempty"`
	}
	type attachment struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	type sendRequest struct {
		Sender      address           `json:"sender"`
		To          []address         `json:"to"`
		CC          []address         `json:"cc,omitempty"`
		BCC         []address         `json:"bcc,omitempty"`
		ReplyTo     *address          `json:"replyTo,omitempty"`
		Subject     string            `json:"subject"`
		HTMLContent string            `json:"htmlContent"`
		Headers     map[string]string `json:"headers,omitempty"`
		Attachments []attachment      `json:"attachment,omitempty"`
		Tags        []string          `json:"tags,omitempty"`
	}
	payload := sendRequest{
		Sender: address{Email: request.FromAddress, Name: request.FromName},
		To:     []address{{Email: request.To}}, Subject: request.Subject, HTMLContent: request.Content,
		Headers: map[string]string{"X-Mailin-custom": "notifuse_message_id:" + request.MessageID},
		Tags:    []string{"notifuse"},
	}
	for _, email := range request.EmailOptions.CC {
		if email != "" {
			payload.CC = append(payload.CC, address{Email: email})
		}
	}
	for _, email := range request.EmailOptions.BCC {
		if email != "" {
			payload.BCC = append(payload.BCC, address{Email: email})
		}
	}
	if request.EmailOptions.ReplyTo != "" {
		payload.ReplyTo = &address{Email: request.EmailOptions.ReplyTo}
	}
	if request.EmailOptions.ListUnsubscribeURL != "" {
		payload.Headers["List-Unsubscribe"] = "<" + request.EmailOptions.ListUnsubscribeURL + ">"
		payload.Headers["List-Unsubscribe-Post"] = "List-Unsubscribe=One-Click"
	}
	for i, item := range request.EmailOptions.Attachments {
		if _, err := item.DecodeContent(); err != nil {
			return fmt.Errorf("attachment %d: failed to decode content: %w", i, err)
		}
		payload.Attachments = append(payload.Attachments, attachment{Name: item.Filename, Content: item.Content})
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode Brevo email: %w", err)
	}
	req, err := s.newRequest(ctx, http.MethodPost, "/v3/smtp/email", request.Provider.Brevo.APIKey, body)
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send email with Brevo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return brevoAPIError(resp)
	}
	var result struct {
		MessageID string `json:"messageId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode Brevo send response: %w", err)
	}
	if request.CapturedMessageID != nil && result.MessageID != "" {
		*request.CapturedMessageID = result.MessageID
	}
	return nil
}
