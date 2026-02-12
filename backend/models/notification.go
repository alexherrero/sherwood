package models

import (
	"encoding/json"
	"time"
)

// NotificationType represents the severity or category of a notification.
type NotificationType string

const (
	NotificationInfo    NotificationType = "info"
	NotificationSuccess NotificationType = "success"
	NotificationWarning NotificationType = "warning"
	NotificationError   NotificationType = "error"
	NotificationTrade   NotificationType = "trade"
)

// Notification represents a system event or alert for the user.
type Notification struct {
	ID        string           `json:"id" db:"id"`
	Type      NotificationType `json:"type" db:"type"`
	Title     string           `json:"title" db:"title"`
	Message   string           `json:"message" db:"message"`
	CreatedAt time.Time        `json:"created_at" db:"created_at"`
	IsRead    bool             `json:"is_read" db:"is_read"`

	// Metadata is stored as JSON text in DB
	Metadata     map[string]interface{} `json:"metadata,omitempty" db:"-"`
	MetadataJSON string                 `json:"-" db:"metadata"`
}

// PrepareForSave serializes Metadata to MetadataJSON
func (n *Notification) PrepareForSave() error {
	if n.Metadata == nil {
		n.MetadataJSON = "{}"
		return nil
	}
	bytes, err := json.Marshal(n.Metadata)
	if err != nil {
		return err
	}
	n.MetadataJSON = string(bytes)
	return nil
}

// PostLoad deserializes MetadataJSON to Metadata
func (n *Notification) PostLoad() error {
	if n.MetadataJSON == "" {
		n.Metadata = make(map[string]interface{})
		return nil
	}
	return json.Unmarshal([]byte(n.MetadataJSON), &n.Metadata)
}
