package audit_logs

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

type AuditLogBackgroundService struct {
	auditLogService *AuditLogService
	logger          *slog.Logger

	hasRun atomic.Bool
}

func (s *AuditLogBackgroundService) Run(ctx context.Context) {
	if s.hasRun.Swap(true) {
		panic(fmt.Sprintf("%T.Run() called multiple times", s))
	}

	s.logger.Info("Starting audit log cleanup background service")

	if ctx.Err() != nil {
		return
	}

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.cleanOldAuditLogs(); err != nil {
				s.logger.Error("Failed to clean old audit logs", "error", err)
			}
		}
	}
}

func (s *AuditLogBackgroundService) cleanOldAuditLogs() error {
	return s.auditLogService.CleanOldAuditLogs()
}
