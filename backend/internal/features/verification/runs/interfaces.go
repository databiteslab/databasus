package verification_runs

import (
	"databasus-backend/internal/features/notifiers"
	notifier_models "databasus-backend/internal/features/notifiers/models"
)

type NotificationSender interface {
	SendNotification(notifier *notifiers.Notifier, notification notifier_models.Notification)
}
