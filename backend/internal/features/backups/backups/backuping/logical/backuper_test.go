package backuping_logical

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	backups_core_logical "databasus-backend/internal/features/backups/backups/core/logical"
	backups_config_logical "databasus-backend/internal/features/backups/config/logical"
	"databasus-backend/internal/features/databases"
	"databasus-backend/internal/features/notifiers"
	notifier_models "databasus-backend/internal/features/notifiers/models"
	"databasus-backend/internal/features/storages"
	users_enums "databasus-backend/internal/features/users/enums"
	users_testing "databasus-backend/internal/features/users/testing"
	workspaces_testing "databasus-backend/internal/features/workspaces/testing"
	cache_utils "databasus-backend/internal/util/cache"
)

func Test_BackupExecuted_NotificationSent(t *testing.T) {
	cache_utils.ClearAllCache()
	user := users_testing.CreateTestUser(users_enums.UserRoleAdmin)
	router := CreateTestRouter()
	workspace := workspaces_testing.CreateTestWorkspace("Test Workspace", user, router)
	storage := storages.CreateTestStorage(workspace.ID)
	notifier := notifiers.CreateTestNotifier(workspace.ID)
	database := databases.CreateTestDatabase(workspace.ID, storage, notifier)
	backups_config_logical.EnableBackupsForTestDatabase(database.ID, storage)

	defer func() {
		// cleanup backups first
		backups, _ := backupRepository.FindByDatabaseID(database.ID)
		for _, backup := range backups {
			backupRepository.DeleteByID(backup.ID)
		}

		databases.RemoveTestDatabase(database)
		time.Sleep(50 * time.Millisecond) // Wait for cascading deletes
		notifiers.RemoveTestNotifier(notifier)
		storages.RemoveTestStorage(storage.ID)
		workspaces_testing.RemoveTestWorkspace(workspace, router)
	}()

	t.Run("BackupFailed_FailNotificationSent", func(t *testing.T) {
		mockNotificationSender := &MockNotificationSender{}
		backuper := CreateTestBackuper()
		backuper.notificationSender = mockNotificationSender
		backuper.createBackupUseCase = &CreateFailedBackupUsecase{}

		// Create a backup record directly that will be looked up by MakeBackup
		backup := &backups_core_logical.LogicalBackup{
			DatabaseID: database.ID,
			StorageID:  storage.ID,
			Status:     backups_core_logical.BackupStatusInProgress,
			CreatedAt:  time.Now().UTC(),
		}
		err := backupRepository.Save(backup)
		assert.NoError(t, err)

		// Set up expectations
		mockNotificationSender.On("SendNotification",
			mock.Anything,
			mock.MatchedBy(func(notification notifier_models.Notification) bool {
				return notification.Type == notifier_models.NotificationTypeBackupFailed &&
					strings.Contains(notification.Heading, "❌ Backup failed") &&
					strings.Contains(notification.Message, "backup failed")
			}),
		).Once()

		backuper.MakeBackup(backup.ID, true)

		// Verify all expectations were met
		mockNotificationSender.AssertExpectations(t)
	})

	t.Run("BackupSuccess_SuccessNotificationSent", func(t *testing.T) {
		mockNotificationSender := &MockNotificationSender{}
		backuper := CreateTestBackuper()
		backuper.notificationSender = mockNotificationSender
		backuper.createBackupUseCase = &CreateSuccessBackupUsecase{}

		// Create a backup record directly that will be looked up by MakeBackup
		backup := &backups_core_logical.LogicalBackup{
			DatabaseID: database.ID,
			StorageID:  storage.ID,
			Status:     backups_core_logical.BackupStatusInProgress,
			CreatedAt:  time.Now().UTC(),
		}
		err := backupRepository.Save(backup)
		assert.NoError(t, err)

		// Set up expectations
		mockNotificationSender.On("SendNotification",
			mock.Anything,
			mock.MatchedBy(func(notification notifier_models.Notification) bool {
				return notification.Type == notifier_models.NotificationTypeBackupSuccess &&
					strings.Contains(notification.Heading, "✅ Backup completed") &&
					strings.Contains(notification.Message, "Backup completed successfully")
			}),
		).Once()

		backuper.MakeBackup(backup.ID, true)

		// Verify all expectations were met
		mockNotificationSender.AssertExpectations(t)
	})

	t.Run("BackupSuccess_VerifyNotificationContent", func(t *testing.T) {
		mockNotificationSender := &MockNotificationSender{}
		backuper := CreateTestBackuper()
		backuper.notificationSender = mockNotificationSender
		backuper.createBackupUseCase = &CreateSuccessBackupUsecase{}

		// Create a backup record directly that will be looked up by MakeBackup
		backup := &backups_core_logical.LogicalBackup{
			DatabaseID: database.ID,
			StorageID:  storage.ID,
			Status:     backups_core_logical.BackupStatusInProgress,
			CreatedAt:  time.Now().UTC(),
		}
		err := backupRepository.Save(backup)
		assert.NoError(t, err)

		// capture arguments
		var capturedNotifier *notifiers.Notifier
		var capturedNotification notifier_models.Notification

		mockNotificationSender.On("SendNotification",
			mock.Anything,
			mock.AnythingOfType("notifier_models.Notification"),
		).Run(func(args mock.Arguments) {
			capturedNotifier = args.Get(0).(*notifiers.Notifier)
			capturedNotification = args.Get(1).(notifier_models.Notification)
		}).Once()

		backuper.MakeBackup(backup.ID, true)

		// Verify expectations were met
		mockNotificationSender.AssertExpectations(t)

		// Additional detailed assertions
		assert.Equal(t, notifier_models.NotificationTypeBackupSuccess, capturedNotification.Type)
		assert.Contains(t, capturedNotification.Heading, "✅ Backup completed")
		assert.Contains(t, capturedNotification.Heading, database.Name)
		assert.Contains(t, capturedNotification.Message, "Backup completed successfully")
		assert.Contains(t, capturedNotification.Message, "10.00 MB")
		assert.Equal(t, notifier.ID, capturedNotifier.ID)
	})
}
