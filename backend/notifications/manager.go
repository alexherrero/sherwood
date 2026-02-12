package notifications

import (
	"fmt"
	"time"

	"github.com/alexherrero/sherwood/backend/data"
	"github.com/alexherrero/sherwood/backend/models"
	"github.com/alexherrero/sherwood/backend/realtime"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Manager handles the lifecycle of system notifications.
type Manager struct {
	store     data.NotificationStore
	wsManager *realtime.WebSocketManager
}

// NewManager creates a new notification manager.
//
// Args:
//   - store: Persistence layer for notifications
//   - wsManager: WebSocket manager for real-time broadcasts (can be nil)
//
// Returns:
//   - *Manager: The new manager instance
func NewManager(store data.NotificationStore, wsManager *realtime.WebSocketManager) *Manager {
	return &Manager{
		store:     store,
		wsManager: wsManager,
	}
}

// Send creates and broadcasts a new notification.
//
// Args:
//   - notifType: Type of notification (info, success, warning, error)
//   - title: Brief summary
//   - message: Detailed content
//   - metadata: Optional key-value context data
//
// Returns:
//   - string: ID of the created notification
//   - error: Any error encountered
func (m *Manager) Send(notifType models.NotificationType, title, message string, metadata map[string]interface{}) (string, error) {
	id := uuid.New().String()

	n := models.Notification{
		ID:        id,
		Type:      notifType,
		Title:     title,
		Message:   message,
		CreatedAt: time.Now(),
		IsRead:    false,
		Metadata:  metadata,
	}

	// Persist
	if err := m.store.SaveNotification(n); err != nil {
		log.Error().Err(err).Msg("Failed to persist notification")
		return "", fmt.Errorf("failed to save: %w", err)
	}

	// Broadcast
	if m.wsManager != nil {
		m.wsManager.Broadcast("notification", n)
	}

	return id, nil
}

// GetHistory retrieves recent notifications.
func (m *Manager) GetHistory(limit, offset int) ([]models.Notification, error) {
	return m.store.GetNotifications(limit, offset)
}

// MarkAsRead marks a notification as read.
func (m *Manager) MarkAsRead(id string) error {
	return m.store.MarkAsRead(id)
}

// MarkAllAsRead marks all notifications as read.
func (m *Manager) MarkAllAsRead() error {
	return m.store.MarkAllAsRead()
}

// Helper methods for common types

func (m *Manager) Info(title, message string) {
	m.Send(models.NotificationInfo, title, message, nil)
}

func (m *Manager) Success(title, message string) {
	m.Send(models.NotificationSuccess, title, message, nil)
}

func (m *Manager) Warning(title, message string) {
	m.Send(models.NotificationWarning, title, message, nil)
}

func (m *Manager) Error(title, message string) {
	m.Send(models.NotificationError, title, message, nil)
}
