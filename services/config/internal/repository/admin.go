package repository

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// ---- AdminReader implementation ----

func (r *GormRepository) ListProviders(ctx context.Context, limit, offset int) ([]Provider, int64, error) {
	limit = clampLimit(limit)
	offset = clampOffset(offset)
	var total int64
	if err := r.db.WithContext(ctx).Model(&Provider{}).Where("status <> ?", "deleted").Count(&total).Error; err != nil {
		return nil, 0, classifyReadErr(err)
	}
	var items []Provider
	if err := r.db.WithContext(ctx).Where("status <> ?", "deleted").Order("id ASC").Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, classifyReadErr(err)
	}
	return items, total, nil
}

func (r *GormRepository) GetProvider(ctx context.Context, id string) (Provider, error) {
	var p Provider
	if err := r.db.WithContext(ctx).Where("id = ? AND status <> ?", id, "deleted").First(&p).Error; err != nil {
		return Provider{}, classifyReadErr(err)
	}
	return p, nil
}

func (r *GormRepository) ListModels(ctx context.Context, limit, offset int) ([]Model, int64, error) {
	limit = clampLimit(limit)
	offset = clampOffset(offset)
	var total int64
	if err := r.db.WithContext(ctx).Model(&Model{}).Where("status <> ?", "deleted").Count(&total).Error; err != nil {
		return nil, 0, classifyReadErr(err)
	}
	var items []Model
	if err := r.db.WithContext(ctx).Where("status <> ?", "deleted").Order("id ASC").Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, classifyReadErr(err)
	}
	return items, total, nil
}

func (r *GormRepository) GetModel(ctx context.Context, id string) (Model, error) {
	var m Model
	if err := r.db.WithContext(ctx).Where("id = ? AND status <> ?", id, "deleted").First(&m).Error; err != nil {
		return Model{}, classifyReadErr(err)
	}
	return m, nil
}

func (r *GormRepository) ListModelIDs(ctx context.Context) ([]string, error) {
	var ids []string
	if err := r.db.WithContext(ctx).Model(&Model{}).Where("status = ?", "active").Pluck("id", &ids).Error; err != nil {
		return nil, classifyReadErr(err)
	}
	return ids, nil
}

func (r *GormRepository) ListAdapters(ctx context.Context, limit, offset int) ([]Adapter, int64, error) {
	limit = clampLimit(limit)
	offset = clampOffset(offset)
	var total int64
	if err := r.db.WithContext(ctx).Model(&Adapter{}).Where("status <> ?", "deleted").Count(&total).Error; err != nil {
		return nil, 0, classifyReadErr(err)
	}
	var items []Adapter
	if err := r.db.WithContext(ctx).Where("status <> ?", "deleted").Order("id ASC").Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, classifyReadErr(err)
	}
	return items, total, nil
}

func (r *GormRepository) GetAdapter(ctx context.Context, id string) (Adapter, error) {
	var a Adapter
	if err := r.db.WithContext(ctx).Where("id = ? AND status <> ?", id, "deleted").First(&a).Error; err != nil {
		return Adapter{}, classifyReadErr(err)
	}
	return a, nil
}

func (r *GormRepository) ListEndpoints(ctx context.Context, providerID string) ([]UpstreamEndpoint, error) {
	var items []UpstreamEndpoint
	if err := r.db.WithContext(ctx).Where("provider_id = ? AND status <> ?", providerID, "deleted").Order("id ASC").Find(&items).Error; err != nil {
		return nil, classifyReadErr(err)
	}
	return items, nil
}

func (r *GormRepository) ListCredentials(ctx context.Context, providerID string) ([]UpstreamCredential, error) {
	var items []UpstreamCredential
	if err := r.db.WithContext(ctx).Where("provider_id = ? AND status <> ?", providerID, "deleted").Order("priority DESC, id ASC").Find(&items).Error; err != nil {
		return nil, classifyReadErr(err)
	}
	return items, nil
}

func (r *GormRepository) ListRoutes(ctx context.Context, limit, offset int) ([]RouteMapping, int64, error) {
	limit = clampLimit(limit)
	offset = clampOffset(offset)
	var total int64
	if err := r.db.WithContext(ctx).Model(&RouteMapping{}).Where("status <> ?", "deleted").Count(&total).Error; err != nil {
		return nil, 0, classifyReadErr(err)
	}
	var items []RouteMapping
	if err := r.db.WithContext(ctx).Where("status <> ?", "deleted").Order("model_id ASC, priority DESC, id ASC").Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, classifyReadErr(err)
	}
	return items, total, nil
}

func (r *GormRepository) GetRoute(ctx context.Context, id string) (RouteMapping, error) {
	var rm RouteMapping
	if err := r.db.WithContext(ctx).Where("id = ? AND status <> ?", id, "deleted").First(&rm).Error; err != nil {
		return RouteMapping{}, classifyReadErr(err)
	}
	return rm, nil
}

func (r *GormRepository) ListRouteCredentials(ctx context.Context, routeID string) ([]RouteCredential, error) {
	var items []RouteCredential
	if err := r.db.WithContext(ctx).Where("route_id = ?", routeID).Order("priority DESC, credential_id ASC").Find(&items).Error; err != nil {
		return nil, classifyReadErr(err)
	}
	return items, nil
}

func (r *GormRepository) ListAllActiveModels(ctx context.Context) ([]Model, error) {
	var items []Model
	if err := r.db.WithContext(ctx).Where("status <> ?", "deleted").Order("id ASC").Find(&items).Error; err != nil {
		return nil, classifyReadErr(err)
	}
	return items, nil
}

func (r *GormRepository) ListAllActiveProviders(ctx context.Context) ([]Provider, error) {
	var items []Provider
	if err := r.db.WithContext(ctx).Where("status <> ?", "deleted").Order("id ASC").Find(&items).Error; err != nil {
		return nil, classifyReadErr(err)
	}
	return items, nil
}

func (r *GormRepository) ListAllActiveRoutes(ctx context.Context) ([]RouteMapping, error) {
	var items []RouteMapping
	if err := r.db.WithContext(ctx).Where("status <> ?", "deleted").Order("model_id ASC, priority DESC, id ASC").Find(&items).Error; err != nil {
		return nil, classifyReadErr(err)
	}
	return items, nil
}

func (r *GormRepository) ListAllActiveAdapters(ctx context.Context) ([]Adapter, error) {
	var items []Adapter
	if err := r.db.WithContext(ctx).Where("status <> ?", "deleted").Order("id ASC").Find(&items).Error; err != nil {
		return nil, classifyReadErr(err)
	}
	return items, nil
}

// ---- AdminWriter implementation ----

// AdminWriter is the write contract for config admin data.
type AdminWriter interface {
	CreateProvider(ctx context.Context, p *Provider) error
	UpdateProvider(ctx context.Context, id string, fields map[string]any) error
	DeleteProvider(ctx context.Context, id string) error

	CreateModel(ctx context.Context, m *Model) error
	UpdateModel(ctx context.Context, id string, fields map[string]any) error
	DeleteModel(ctx context.Context, id string) error

	CreateAdapter(ctx context.Context, a *Adapter) error
	UpdateAdapter(ctx context.Context, id string, fields map[string]any) error
	DeleteAdapter(ctx context.Context, id string) error

	CreateEndpoint(ctx context.Context, e *UpstreamEndpoint) error
	UpdateEndpoint(ctx context.Context, id int64, fields map[string]any) error
	DeleteEndpoint(ctx context.Context, id int64) error

	CreateCredential(ctx context.Context, c *UpstreamCredential) error
	UpdateCredential(ctx context.Context, id string, fields map[string]any) error
	DeleteCredential(ctx context.Context, id string) error

	CreateRoute(ctx context.Context, rm *RouteMapping) error
	UpdateRoute(ctx context.Context, id string, fields map[string]any) error
	DeleteRoute(ctx context.Context, id string) error
	SetRouteCredentials(ctx context.Context, routeID string, creds []RouteCredential) error

	// SetGlobalConfigEntry upserts a single global_config row.
	SetGlobalConfigEntry(ctx context.Context, key string, value []byte, updatedBy string) error
}

func (r *GormRepository) CreateProvider(ctx context.Context, p *Provider) error {
	if err := r.db.WithContext(ctx).Create(p).Error; err != nil {
		return classifyWriteErr(err)
	}
	return nil
}

func (r *GormRepository) UpdateProvider(ctx context.Context, id string, fields map[string]any) error {
	return updateByID(ctx, r.db, &Provider{}, "id", id, fields)
}

func (r *GormRepository) DeleteProvider(ctx context.Context, id string) error {
	return softDeleteByID(ctx, r.db, &Provider{}, "id", id)
}

func (r *GormRepository) CreateModel(ctx context.Context, m *Model) error {
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		return classifyWriteErr(err)
	}
	return nil
}

func (r *GormRepository) UpdateModel(ctx context.Context, id string, fields map[string]any) error {
	return updateByID(ctx, r.db, &Model{}, "id", id, fields)
}

func (r *GormRepository) DeleteModel(ctx context.Context, id string) error {
	return softDeleteByID(ctx, r.db, &Model{}, "id", id)
}

func (r *GormRepository) CreateAdapter(ctx context.Context, a *Adapter) error {
	if err := r.db.WithContext(ctx).Create(a).Error; err != nil {
		return classifyWriteErr(err)
	}
	return nil
}

func (r *GormRepository) UpdateAdapter(ctx context.Context, id string, fields map[string]any) error {
	return updateByID(ctx, r.db, &Adapter{}, "id", id, fields)
}

func (r *GormRepository) DeleteAdapter(ctx context.Context, id string) error {
	return softDeleteByID(ctx, r.db, &Adapter{}, "id", id)
}

func (r *GormRepository) CreateEndpoint(ctx context.Context, e *UpstreamEndpoint) error {
	if err := r.db.WithContext(ctx).Create(e).Error; err != nil {
		return classifyWriteErr(err)
	}
	return nil
}

func (r *GormRepository) UpdateEndpoint(ctx context.Context, id int64, fields map[string]any) error {
	return updateByID(ctx, r.db, &UpstreamEndpoint{}, "id", id, fields)
}

func (r *GormRepository) DeleteEndpoint(ctx context.Context, id int64) error {
	return softDeleteByID(ctx, r.db, &UpstreamEndpoint{}, "id", id)
}

func (r *GormRepository) CreateCredential(ctx context.Context, c *UpstreamCredential) error {
	if err := r.db.WithContext(ctx).Create(c).Error; err != nil {
		return classifyWriteErr(err)
	}
	return nil
}

func (r *GormRepository) UpdateCredential(ctx context.Context, id string, fields map[string]any) error {
	return updateByID(ctx, r.db, &UpstreamCredential{}, "id", id, fields)
}

func (r *GormRepository) DeleteCredential(ctx context.Context, id string) error {
	return softDeleteByID(ctx, r.db, &UpstreamCredential{}, "id", id)
}

func (r *GormRepository) CreateRoute(ctx context.Context, rm *RouteMapping) error {
	if err := r.db.WithContext(ctx).Create(rm).Error; err != nil {
		return classifyWriteErr(err)
	}
	return nil
}

func (r *GormRepository) UpdateRoute(ctx context.Context, id string, fields map[string]any) error {
	return updateByID(ctx, r.db, &RouteMapping{}, "id", id, fields)
}

func (r *GormRepository) DeleteRoute(ctx context.Context, id string) error {
	return softDeleteByID(ctx, r.db, &RouteMapping{}, "id", id)
}

func (r *GormRepository) SetRouteCredentials(ctx context.Context, routeID string, creds []RouteCredential) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("route_id = ?", routeID).Delete(&RouteCredential{}).Error; err != nil {
			return classifyWriteErr(err)
		}
		if len(creds) == 0 {
			return nil
		}
		for i := range creds {
			creds[i].RouteID = routeID
		}
		if err := tx.Create(&creds).Error; err != nil {
			return classifyWriteErr(err)
		}
		return nil
	})
}

// ---- helpers ----

func updateByID(ctx context.Context, db *gorm.DB, model any, col string, id any, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	// GORM auto-updates updated_at for models with the field; the DB also has
	// a touch_updated_at trigger. Do NOT set updated_at in the map — it causes
	// "multiple assignments to same column" (SQLSTATE 42601).
	delete(fields, "updated_at")
	res := db.WithContext(ctx).Model(model).Where(col+" = ?", id).Updates(fields)
	if res.Error != nil {
		return classifyWriteErr(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func softDeleteByID(ctx context.Context, db *gorm.DB, model any, col string, id any) error {
	res := db.WithContext(ctx).Model(model).Where(col+" = ?", id).Updates(map[string]any{
		"status":     "deleted",
		"updated_at": gorm.Expr("now()"),
	})
	if res.Error != nil {
		return classifyWriteErr(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func classifyReadErr(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return ErrQueryFailed
}

// classifyWriteErr maps a Postgres driver error to a stable sentinel by
// string-matching the SQLSTATE code (the same approach as isUniqueViolation).
// We do not import the pgx/pgconn type to avoid a new dependency; the
// SQLSTATE is stable and surfaces in the error string.
//
//   - 23505 (unique_violation)         → ErrConflict (409)
//   - 23502 (not_null_violation)       → ErrInvalidInput (400)
//   - 23503 (foreign_key_violation)    → ErrInvalidInput (400)
//   - 23514 (check_violation)          → ErrInvalidInput (400)
//   - 42703 (undefined_column)         → ErrInvalidInput (400) — defensive;
//     allowlist filtering should prevent
//     this, but classify as client error
//     if it slips through.
//   - everything else                  → ErrInsertFailed (500)
func classifyWriteErr(err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	lower := strings.ToLower(s)
	if strings.Contains(s, "23505") || strings.Contains(lower, "unique constraint") {
		return ErrConflict
	}
	if strings.Contains(s, "23502") || strings.Contains(s, "23503") || strings.Contains(s, "23514") || strings.Contains(s, "42703") {
		return ErrInvalidInput
	}
	return ErrInsertFailed
}
