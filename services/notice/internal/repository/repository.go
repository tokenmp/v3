// Package repository provides the data-access layer for the notice service.
//
// It returns stable classified sentinel errors (ErrNotFound / ErrInternal)
// that never wrap the underlying driver error, so the public error surface
// is safe to log and maps deterministically to HTTP status codes.
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/tokenmp/v3/services/notice/internal/models"
)

// Sentinel errors. They do not wrap the driver error.
var (
	ErrNotFound = errors.New("repository: not found")
	ErrInternal = errors.New("repository: internal error")
)

// classifiedError hides the driver cause behind a stable sentinel.
type classifiedError struct {
	sentinel error
	driver   error
}

func (e *classifiedError) Error() string { return e.sentinel.Error() }
func (e *classifiedError) Unwrap() error { return e.sentinel }
func (e *classifiedError) cause() error  { return e.driver }

// Repository is the notice service data-access layer.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository backed by db.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// clampLimit coerces limit into the 1..100 contract range.
func clampLimit(limit int) int {
	if limit < 1 {
		return 1
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func clampOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

// ListAnnouncements returns published announcements newest-first.
func (r *Repository) ListAnnouncements(ctx context.Context, limit, offset int) ([]models.Announcement, int, error) {
	limit = clampLimit(limit)
	offset = clampOffset(offset)
	var items []models.Announcement
	q := r.db.WithContext(ctx).
		Where("published_at <= ?", time.Now().UTC()).
		Order("published_at DESC")
	var total int64
	if err := q.Model(&models.Announcement{}).Count(&total).Error; err != nil {
		return nil, 0, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	if err := q.Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	return items, int(total), nil
}

// GetAnnouncement returns a single announcement by id.
func (r *Repository) GetAnnouncement(ctx context.Context, id string) (models.Announcement, error) {
	var a models.Announcement
	err := r.db.WithContext(ctx).
		Where("id = ? AND published_at <= ?", id, time.Now().UTC()).
		First(&a).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.Announcement{}, ErrNotFound
		}
		return models.Announcement{}, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	return a, nil
}

// ListChangelogs returns published changelogs newest-first.
func (r *Repository) ListChangelogs(ctx context.Context, limit, offset int) ([]models.Changelog, int, error) {
	limit = clampLimit(limit)
	offset = clampOffset(offset)
	var items []models.Changelog
	q := r.db.WithContext(ctx).
		Where("published_at <= ?", time.Now().UTC()).
		Order("published_at DESC")
	var total int64
	if err := q.Model(&models.Changelog{}).Count(&total).Error; err != nil {
		return nil, 0, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	if err := q.Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	return items, int(total), nil
}

// GetChangelog returns a single changelog by id.
func (r *Repository) GetChangelog(ctx context.Context, id string) (models.Changelog, error) {
	var c models.Changelog
	err := r.db.WithContext(ctx).
		Where("id = ? AND published_at <= ?", id, time.Now().UTC()).
		First(&c).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.Changelog{}, ErrNotFound
		}
		return models.Changelog{}, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	return c, nil
}

// ListNotifications returns notifications for userID newest-first.
func (r *Repository) ListNotifications(ctx context.Context, userID string, limit, offset int) ([]models.Notification, int, error) {
	limit = clampLimit(limit)
	offset = clampOffset(offset)
	var items []models.Notification
	q := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC")
	var total int64
	if err := q.Model(&models.Notification{}).Count(&total).Error; err != nil {
		return nil, 0, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	if err := q.Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	return items, int(total), nil
}

// UnreadCount returns the number of unread notifications for userID.
func (r *Repository) UnreadCount(ctx context.Context, userID string) (int, error) {
	var total int64
	err := r.db.WithContext(ctx).
		Model(&models.Notification{}).
		Where("user_id = ? AND read_at IS NULL", userID).
		Count(&total).Error
	if err != nil {
		return 0, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	return int(total), nil
}

// MarkRead marks a single notification as read for userID. Idempotent: an
// already-read notification still returns nil. A notification not owned by
// userID returns ErrNotFound (not a read error) to avoid cross-user probing.
func (r *Repository) MarkRead(ctx context.Context, userID, id string) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&models.Notification{}).
		Where("user_id = ? AND id = ?", userID, id).
		Update("read_at", &now)
	if res.Error != nil {
		return &classifiedError{sentinel: ErrInternal, driver: res.Error}
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAllRead marks all notifications as read for userID. Idempotent.
func (r *Repository) MarkAllRead(ctx context.Context, userID string) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&models.Notification{}).
		Where("user_id = ? AND read_at IS NULL", userID).
		Update("read_at", &now)
	if res.Error != nil {
		return &classifiedError{sentinel: ErrInternal, driver: res.Error}
	}
	return nil
}

// ParseUUID validates that s is a well-formed UUID. Returns a stable error
// when it is not, so callers can map malformed path params to 404 without
// hitting the database.
func ParseUUID(s string) error {
	if len(s) != 36 {
		return fmt.Errorf("%w: invalid uuid", ErrNotFound)
	}
	// 8-4-4-4-12 with dashes at the expected positions.
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return fmt.Errorf("%w: invalid uuid", ErrNotFound)
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return fmt.Errorf("%w: invalid uuid", ErrNotFound)
			}
		}
	}
	return nil
}
