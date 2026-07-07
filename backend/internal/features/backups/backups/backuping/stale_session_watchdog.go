package backuping

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"databasus-backend/internal/features/databases"
	"databasus-backend/internal/util/encryption"
)

const staleSessionWatchdogQueryTimeout = 30 * time.Second

// StaleSessionWatchdog periodically scans every configured PostgreSQL database for
// pg_dump backend sessions that have been running far longer than expected and
// terminates them.
//
// This exists because pg_dump explicitly disables statement_timeout and
// idle_in_transaction_session_timeout on its own connection (see PostgreSQL's
// pg_dump.c setup_connection()), so neither server-side defaults nor client-side
// connection settings can bound a stuck pg_dump session. A crashed backup process, a
// dropped network connection, or a stalled COPY can therefore leave a pg_dump backend
// open for many hours, holding an AccessShareLock that blocks schema migrations
// (ALTER TABLE requires AccessExclusiveLock). See GetBackupsScheduler for the job
// scheduling side of backups.
//
// Trade-offs to be aware of when tuning maxSessionAge/checkInterval:
//   - Every check opens a fresh connection to every configured PostgreSQL database,
//     even if that database has no backup running. For deployments with a very large
//     number of databases, this adds a recurring connection burst; consider raising
//     checkInterval if that becomes a concern.
//   - A database that is temporarily unreachable (firewall window, paused instance,
//     etc.) will fail this check on every tick and log a warning until it becomes
//     reachable again - this is expected and does not affect backups.
//   - maxSessionAge must be set higher than any legitimate backup's expected
//     duration, or this watchdog will kill still-progressing backups.
type StaleSessionWatchdog struct {
	databaseService *databases.DatabaseService
	fieldEncryptor  encryption.FieldEncryptor
	logger          *slog.Logger
	isEnabled       bool
	checkInterval   time.Duration
	maxSessionAge   time.Duration

	runOnce sync.Once
	hasRun  atomic.Bool
}

func (w *StaleSessionWatchdog) Run(ctx context.Context) {
	wasAlreadyRun := w.hasRun.Load()

	w.runOnce.Do(func() {
		w.hasRun.Store(true)

		if ctx.Err() != nil {
			return
		}

		if !w.isEnabled {
			w.logger.Info("Stale pg_dump session watchdog is disabled via configuration")
			return
		}

		w.logger.Info(
			"Stale pg_dump session watchdog started",
			"checkInterval", w.checkInterval,
			"maxSessionAge", w.maxSessionAge,
		)

		ticker := time.NewTicker(w.checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.checkAllDatabases(ctx)
			}
		}
	})

	if wasAlreadyRun {
		panic(fmt.Sprintf("%T.Run() called multiple times", w))
	}
}

func (w *StaleSessionWatchdog) checkAllDatabases(ctx context.Context) {
	allDatabases, err := w.databaseService.GetAllDatabases()
	if err != nil {
		w.logger.Error("Stale session watchdog failed to load databases", "error", err)
		return
	}

	for _, database := range allDatabases {
		if database.Postgresql == nil {
			continue
		}

		w.checkSingleDatabase(ctx, database)
	}
}

func (w *StaleSessionWatchdog) checkSingleDatabase(
	ctx context.Context,
	database *databases.Database,
) {
	checkCtx, cancel := context.WithTimeout(ctx, staleSessionWatchdogQueryTimeout)
	defer cancel()

	terminatedCount, err := database.Postgresql.TerminateStaleBackupSessions(
		checkCtx,
		w.logger,
		w.fieldEncryptor,
		database.ID,
		w.maxSessionAge,
	)
	if err != nil {
		w.logger.Warn(
			"Stale session watchdog failed to check database for stuck pg_dump sessions",
			"databaseId", database.ID,
			"error", err,
		)
		return
	}

	if terminatedCount > 0 {
		w.logger.Warn(
			"Stale session watchdog terminated stuck pg_dump sessions",
			"databaseId", database.ID,
			"terminatedCount", terminatedCount,
			"maxSessionAge", w.maxSessionAge,
		)
	}
}
