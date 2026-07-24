// Package models defines the GORM application-layer models for the notice
// service. The database schema (in services/notice/migrations) is the source
// of truth; these models mirror it for query/mapping purposes only.
// AutoMigrate is forbidden.
package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// NotificationAction is the generic, data-driven affordance stored as JSONB
// on a notification. It is nullable at the database level (NULL means the
// notification is purely informational). The client renders the action purely
// from its fields; no target is ever hardcoded by the application.
type NotificationAction struct {
	Type  string `json:"type"`            // e.g. "link"
	Label string `json:"label"`           // human-readable label
	Href  string `json:"href,omitempty"`  // client route, for "link"
}

// NotificationActionPtr wraps *NotificationAction so GORM can scan a nullable
// JSONB column. A NULL column scans into a nil pointer (no action).
type NotificationActionPtr struct {
	Action *NotificationAction
	IsNull bool
}

// Value implements driver.Valuer. A nil action serializes to SQL NULL.
func (n NotificationActionPtr) Value() (driver.Value, error) {
	if n.Action == nil || n.IsNull {
		return nil, nil
	}
	return json.Marshal(n.Action)
}

// Scan implements sql.Scanner. A nil/NULL source yields a nil action.
func (n *NotificationActionPtr) Scan(src any) error {
	if src == nil {
		n.Action = nil
		n.IsNull = true
		return nil
	}
	var raw []byte
	switch v := src.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return errors.New("notification action: unsupported scan source")
	}
	if len(raw) == 0 {
		n.Action = nil
		n.IsNull = true
		return nil
	}
	var a NotificationAction
	if err := json.Unmarshal(raw, &a); err != nil {
		return err
	}
	n.Action = &a
	n.IsNull = false
	return nil
}

// Announcement mirrors the announcements table.
type Announcement struct {
	ID          string    `gorm:"column:id;primaryKey" json:"id"`
	Title       string    `gorm:"column:title" json:"title"`
	Summary     string    `gorm:"column:summary" json:"summary"`
	Body        string    `gorm:"column:body" json:"body"`
	Severity    string    `gorm:"column:severity" json:"severity"`
	PublishedAt time.Time `gorm:"column:published_at" json:"published_at"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"-"`
	UpdatedAt   time.Time `gorm:"column:updated_at" json:"-"`
}

func (Announcement) TableName() string { return "announcements" }

// Changelog mirrors the changelogs table.
type Changelog struct {
	ID          string    `gorm:"column:id;primaryKey" json:"id"`
	Version     string    `gorm:"column:version" json:"version"`
	Title       string    `gorm:"column:title" json:"title"`
	Body        string    `gorm:"column:body" json:"body"`
	PublishedAt time.Time `gorm:"column:published_at" json:"published_at"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"-"`
	UpdatedAt   time.Time `gorm:"column:updated_at" json:"-"`
}

func (Changelog) TableName() string { return "changelogs" }

// Notification mirrors the notifications table.
type Notification struct {
	ID        string                  `gorm:"column:id;primaryKey" json:"id"`
	UserID    string                  `gorm:"column:user_id" json:"-"`
	Type      string                  `gorm:"column:type" json:"type"`
	Title     string                  `gorm:"column:title" json:"title"`
	Body      string                  `gorm:"column:body" json:"body"`
	Action    NotificationActionPtr   `gorm:"column:action" json:"-"`
	ReadAt    *time.Time              `gorm:"column:read_at" json:"read_at"`
	CreatedAt time.Time               `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time               `gorm:"column:updated_at" json:"-"`
}

func (Notification) TableName() string { return "notifications" }

// Page is a generic paginated response envelope.
type Page[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}
