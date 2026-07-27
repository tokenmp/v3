package repository

import (
	"context"
	"time"

	"github.com/tokenmp/v3/services/notice/internal/models"
)

// ---- Announcement admin ----

// ListAllAnnouncements returns all announcements (including drafts) newest-first.
func (r *Repository) ListAllAnnouncements(ctx context.Context, limit, offset int) ([]models.Announcement, int, error) {
	limit = clampLimit(limit)
	offset = clampOffset(offset)
	q := r.db.WithContext(ctx).Order("created_at DESC, id DESC")
	var total int64
	if err := q.Model(&models.Announcement{}).Count(&total).Error; err != nil {
		return nil, 0, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	var items []models.Announcement
	if err := q.Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	return items, int(total), nil
}

// CreateAnnouncement inserts a new announcement row.
func (r *Repository) CreateAnnouncement(ctx context.Context, a *models.Announcement) error {
	if err := r.db.WithContext(ctx).Omit("id").Create(a).Error; err != nil {
		return &classifiedError{sentinel: ErrInternal, driver: err}
	}
	return nil
}

// UpdateAnnouncement modifies mutable columns of an announcement.
func (r *Repository) UpdateAnnouncement(ctx context.Context, id string, fields map[string]any) error {
	res := r.db.WithContext(ctx).Model(&models.Announcement{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return &classifiedError{sentinel: ErrInternal, driver: res.Error}
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAnnouncement removes an announcement by id.
func (r *Repository) DeleteAnnouncement(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&models.Announcement{})
	if res.Error != nil {
		return &classifiedError{sentinel: ErrInternal, driver: res.Error}
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// PublishAnnouncement sets published_at to now for an announcement.
func (r *Repository) PublishAnnouncement(ctx context.Context, id string) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&models.Announcement{}).Where("id = ?", id).Update("published_at", now)
	if res.Error != nil {
		return &classifiedError{sentinel: ErrInternal, driver: res.Error}
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Changelog admin ----

// ListAllChangelogs returns all changelogs (including drafts) newest-first.
func (r *Repository) ListAllChangelogs(ctx context.Context, limit, offset int) ([]models.Changelog, int, error) {
	limit = clampLimit(limit)
	offset = clampOffset(offset)
	q := r.db.WithContext(ctx).Order("created_at DESC, id DESC")
	var total int64
	if err := q.Model(&models.Changelog{}).Count(&total).Error; err != nil {
		return nil, 0, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	var items []models.Changelog
	if err := q.Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	return items, int(total), nil
}

// CreateChangelog inserts a new changelog row.
func (r *Repository) CreateChangelog(ctx context.Context, c *models.Changelog) error {
	if err := r.db.WithContext(ctx).Omit("id").Create(c).Error; err != nil {
		return &classifiedError{sentinel: ErrInternal, driver: err}
	}
	return nil
}

// UpdateChangelog modifies mutable columns of a changelog.
func (r *Repository) UpdateChangelog(ctx context.Context, id string, fields map[string]any) error {
	res := r.db.WithContext(ctx).Model(&models.Changelog{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return &classifiedError{sentinel: ErrInternal, driver: res.Error}
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteChangelog removes a changelog by id.
func (r *Repository) DeleteChangelog(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&models.Changelog{})
	if res.Error != nil {
		return &classifiedError{sentinel: ErrInternal, driver: res.Error}
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// PublishChangelog sets published_at to now for a changelog.
func (r *Repository) PublishChangelog(ctx context.Context, id string) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&models.Changelog{}).Where("id = ?", id).Update("published_at", now)
	if res.Error != nil {
		return &classifiedError{sentinel: ErrInternal, driver: res.Error}
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Notification admin ----

// ListAllNotifications returns all notifications across all users newest-first.
func (r *Repository) ListAllNotifications(ctx context.Context, limit, offset int) ([]models.Notification, int, error) {
	limit = clampLimit(limit)
	offset = clampOffset(offset)
	q := r.db.WithContext(ctx).Order("created_at DESC, id DESC")
	var total int64
	if err := q.Model(&models.Notification{}).Count(&total).Error; err != nil {
		return nil, 0, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	var items []models.Notification
	if err := q.Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, &classifiedError{sentinel: ErrInternal, driver: err}
	}
	return items, int(total), nil
}

// CreateNotification inserts a new notification row.
func (r *Repository) CreateNotification(ctx context.Context, n *models.Notification) error {
	if err := r.db.WithContext(ctx).Omit("id").Create(n).Error; err != nil {
		return &classifiedError{sentinel: ErrInternal, driver: err}
	}
	return nil
}

// DeleteNotification removes a notification by id.
func (r *Repository) DeleteNotification(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&models.Notification{})
	if res.Error != nil {
		return &classifiedError{sentinel: ErrInternal, driver: res.Error}
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
