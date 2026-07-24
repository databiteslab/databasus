package users_interfaces

import (
	"github.com/google/uuid"
)

type AuditLogWriter interface {
	WriteAuditLog(message string, userID, workspaceID *uuid.UUID)
}

type EmailSender interface {
	SendEmail(to, subject, body string) error
}
