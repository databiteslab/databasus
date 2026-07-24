package webhook_notifier

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	notifier_models "databasus-backend/internal/features/notifiers/models"
)

type passthroughEncryptor struct{}

func (p passthroughEncryptor) Encrypt(plaintext string) (string, error) {
	return plaintext, nil
}

func (p passthroughEncryptor) Decrypt(ciphertext string) (string, error) {
	return ciphertext, nil
}

type capturedRequest struct {
	method  string
	query   url.Values
	headers http.Header
	body    string
}

type requestRecorder struct {
	mu       sync.Mutex
	requests []capturedRequest
}

func (r *requestRecorder) add(request capturedRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.requests = append(r.requests, request)
}

func (r *requestRecorder) GetRequestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.requests)
}

func (r *requestRecorder) GetLastRequest() capturedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.requests[len(r.requests)-1]
}

func startRecordingWebhook(t *testing.T, statusCode int) (string, *requestRecorder) {
	recorder := &requestRecorder{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		recorder.add(capturedRequest{
			method:  req.Method,
			query:   req.URL.Query(),
			headers: req.Header.Clone(),
			body:    string(body),
		})

		w.WriteHeader(statusCode)
	}))
	t.Cleanup(server.Close)

	return server.URL, recorder
}

func send(t *testing.T, notifier *WebhookNotifier, notificationType notifier_models.NotificationType) error {
	t.Helper()

	return notifier.Send(
		passthroughEncryptor{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		notifier_models.Notification{
			Type:    notificationType,
			Heading: "Backup completed",
			Message: "All good",
		},
	)
}

func acceptAll() []notifier_models.NotificationType {
	return []notifier_models.NotificationType{notifier_models.NotificationTypeAll}
}

func Test_Send_WithPOSTAndNoBodyTemplate_SendsDefaultJSONBody(t *testing.T) {
	webhookURL, recorder := startRecordingWebhook(t, http.StatusOK)
	notifier := &WebhookNotifier{
		WebhookURL:              webhookURL,
		WebhookMethod:           WebhookMethodPOST,
		AcceptNotificationTypes: acceptAll(),
	}

	require.NoError(t, send(t, notifier, notifier_models.NotificationTypeBackupSuccess))

	require.Equal(t, 1, recorder.GetRequestCount())
	request := recorder.GetLastRequest()
	assert.Equal(t, http.MethodPost, request.method)
	assert.Equal(t, "application/json", request.headers.Get("Content-Type"))

	var payload map[string]string
	require.NoError(t, json.Unmarshal([]byte(request.body), &payload))
	assert.Equal(t, "Backup completed", payload["heading"])
	assert.Equal(t, "All good", payload["message"])
}

func Test_Send_WithPOSTAndBodyTemplate_SubstitutesAndEscapesPlaceholders(t *testing.T) {
	webhookURL, recorder := startRecordingWebhook(t, http.StatusOK)
	template := `{"title":"{{heading}}","text":"{{message}}"}`
	notifier := &WebhookNotifier{
		WebhookURL:              webhookURL,
		WebhookMethod:           WebhookMethodPOST,
		BodyTemplate:            &template,
		AcceptNotificationTypes: acceptAll(),
	}

	err := notifier.Send(
		passthroughEncryptor{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		notifier_models.Notification{
			Type:    notifier_models.NotificationTypeBackupSuccess,
			Heading: `He said "hi"`,
			Message: "line1\nline2",
		},
	)
	require.NoError(t, err)

	var payload map[string]string
	require.NoError(t, json.Unmarshal([]byte(recorder.GetLastRequest().body), &payload))
	assert.Equal(t, `He said "hi"`, payload["title"])
	assert.Equal(t, "line1\nline2", payload["text"])
}

func Test_Send_WithCustomContentTypeHeader_DoesNotOverrideIt(t *testing.T) {
	webhookURL, recorder := startRecordingWebhook(t, http.StatusOK)
	notifier := &WebhookNotifier{
		WebhookURL:              webhookURL,
		WebhookMethod:           WebhookMethodPOST,
		Headers:                 []WebhookHeader{{Key: "Content-Type", Value: "application/xml"}},
		AcceptNotificationTypes: acceptAll(),
	}

	require.NoError(t, send(t, notifier, notifier_models.NotificationTypeBackupSuccess))

	assert.Equal(t, "application/xml", recorder.GetLastRequest().headers.Get("Content-Type"))
}

func Test_Send_WithGET_SendsHeadingAndMessageAsQueryParams(t *testing.T) {
	webhookURL, recorder := startRecordingWebhook(t, http.StatusOK)
	notifier := &WebhookNotifier{
		WebhookURL:              webhookURL,
		WebhookMethod:           WebhookMethodGET,
		AcceptNotificationTypes: acceptAll(),
	}

	require.NoError(t, send(t, notifier, notifier_models.NotificationTypeBackupSuccess))

	request := recorder.GetLastRequest()
	assert.Equal(t, http.MethodGet, request.method)
	assert.Equal(t, "Backup completed", request.query.Get("heading"))
	assert.Equal(t, "All good", request.query.Get("message"))
	assert.Empty(t, request.body)
}

func Test_Send_WithCustomHeaders_AppliesThemToRequest(t *testing.T) {
	webhookURL, recorder := startRecordingWebhook(t, http.StatusOK)
	notifier := &WebhookNotifier{
		WebhookURL:              webhookURL,
		WebhookMethod:           WebhookMethodPOST,
		Headers:                 []WebhookHeader{{Key: "X-Api-Key", Value: "secret-value"}},
		AcceptNotificationTypes: acceptAll(),
	}

	require.NoError(t, send(t, notifier, notifier_models.NotificationTypeBackupSuccess))

	assert.Equal(t, "secret-value", recorder.GetLastRequest().headers.Get("X-Api-Key"))
}

func Test_Send_WhenServerReturnsNon2xx_ReturnsError(t *testing.T) {
	webhookURL, _ := startRecordingWebhook(t, http.StatusInternalServerError)
	notifier := &WebhookNotifier{
		WebhookURL:              webhookURL,
		WebhookMethod:           WebhookMethodPOST,
		AcceptNotificationTypes: acceptAll(),
	}

	require.Error(t, send(t, notifier, notifier_models.NotificationTypeBackupSuccess))
}

func Test_Send_WhenURLUnreachable_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	unreachableURL := server.URL
	server.Close()

	notifier := &WebhookNotifier{
		WebhookURL:              unreachableURL,
		WebhookMethod:           WebhookMethodPOST,
		AcceptNotificationTypes: acceptAll(),
	}

	require.Error(t, send(t, notifier, notifier_models.NotificationTypeBackupSuccess))
}

func Test_Send_WhenAcceptTypesEmpty_SendsEveryType(t *testing.T) {
	webhookURL, recorder := startRecordingWebhook(t, http.StatusOK)
	notifier := &WebhookNotifier{
		WebhookURL:    webhookURL,
		WebhookMethod: WebhookMethodPOST,
	}

	require.NoError(t, send(t, notifier, notifier_models.NotificationTypeBackupSuccess))
	require.NoError(t, send(t, notifier, notifier_models.NotificationTypeHealthcheckFailed))

	assert.Equal(t, 2, recorder.GetRequestCount())
}

func Test_Send_WhenAcceptContainsAll_SendsEveryType(t *testing.T) {
	webhookURL, recorder := startRecordingWebhook(t, http.StatusOK)
	notifier := &WebhookNotifier{
		WebhookURL:              webhookURL,
		WebhookMethod:           WebhookMethodPOST,
		AcceptNotificationTypes: acceptAll(),
	}

	require.NoError(t, send(t, notifier, notifier_models.NotificationTypeBackupSuccess))
	require.NoError(t, send(t, notifier, notifier_models.NotificationTypeVerificationFailed))

	assert.Equal(t, 2, recorder.GetRequestCount())
}

func Test_Send_WhenNarrowedToBackupSuccess_SkipsOtherTypes(t *testing.T) {
	webhookURL, recorder := startRecordingWebhook(t, http.StatusOK)
	notifier := &WebhookNotifier{
		WebhookURL:              webhookURL,
		WebhookMethod:           WebhookMethodPOST,
		AcceptNotificationTypes: []notifier_models.NotificationType{notifier_models.NotificationTypeBackupSuccess},
	}

	require.NoError(t, send(t, notifier, notifier_models.NotificationTypeBackupFailed))
	require.NoError(t, send(t, notifier, notifier_models.NotificationTypeHealthcheckSuccess))
	assert.Equal(t, 0, recorder.GetRequestCount())

	require.NoError(t, send(t, notifier, notifier_models.NotificationTypeBackupSuccess))
	assert.Equal(t, 1, recorder.GetRequestCount())
}

func Test_Send_WhenNarrowed_StillSendsWildcardTestNotification(t *testing.T) {
	webhookURL, recorder := startRecordingWebhook(t, http.StatusOK)
	notifier := &WebhookNotifier{
		WebhookURL:              webhookURL,
		WebhookMethod:           WebhookMethodPOST,
		AcceptNotificationTypes: []notifier_models.NotificationType{notifier_models.NotificationTypeBackupFailed},
	}

	require.NoError(t, send(t, notifier, notifier_models.NotificationTypeAll))

	assert.Equal(t, 1, recorder.GetRequestCount())
}

func Test_BeforeSave_WhenAcceptTypesEmpty_DefaultsToAll(t *testing.T) {
	notifier := &WebhookNotifier{
		WebhookURL:    "https://example.com/webhook",
		WebhookMethod: WebhookMethodPOST,
	}

	require.NoError(t, notifier.BeforeSave(nil))

	assert.Equal(t, `["ALL"]`, notifier.AcceptNotificationTypesJSON)
	assert.Equal(t, acceptAll(), notifier.AcceptNotificationTypes)
}

func Test_BeforeSave_WithSingleType_SerializesOnlyThatType(t *testing.T) {
	notifier := &WebhookNotifier{
		WebhookURL:              "https://example.com/webhook",
		WebhookMethod:           WebhookMethodPOST,
		AcceptNotificationTypes: []notifier_models.NotificationType{notifier_models.NotificationTypeBackupSuccess},
	}

	require.NoError(t, notifier.BeforeSave(nil))

	assert.Equal(t, `["BACKUP_SUCCESS"]`, notifier.AcceptNotificationTypesJSON)
}

func Test_AfterFind_WithSerializedTypes_RestoresSlice(t *testing.T) {
	notifier := &WebhookNotifier{
		AcceptNotificationTypesJSON: `["VERIFICATION_FAILED"]`,
	}

	require.NoError(t, notifier.AfterFind(nil))

	assert.Equal(
		t,
		[]notifier_models.NotificationType{notifier_models.NotificationTypeVerificationFailed},
		notifier.AcceptNotificationTypes,
	)
}
