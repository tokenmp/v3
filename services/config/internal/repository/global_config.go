package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GlobalConfigKey enumerates the well-known keys stored in the
// global_config KV table. They align with the V3 GlobalPolicy fields.
type GlobalConfigKey string

const (
	GlobalKeyDefaultRetry   GlobalConfigKey = "default_retry"
	GlobalKeyDefaultTimeout GlobalConfigKey = "default_timeout"
	GlobalKeyAutoModelIDs   GlobalConfigKey = "auto_model_ids"
)

// GlobalConfig maps a row in the global_config KV table.
type GlobalConfig struct {
	Key       string    `gorm:"column:key;primaryKey" json:"key"`
	Value     []byte    `gorm:"column:value;type:jsonb" json:"value"`
	UpdatedBy string    `gorm:"column:updated_by" json:"updated_by,omitempty"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (GlobalConfig) TableName() string { return "global_config" }

// GlobalPolicy is the aggregate view of all three global_config rows,
// decoded as raw JSON bytes (the caller unmarshals into its own types).
// A missing row yields nil bytes (the caller applies defaults).
type GlobalPolicy struct {
	DefaultRetry   json.RawMessage `json:"default_retry,omitempty"`
	DefaultTimeout json.RawMessage `json:"default_timeout,omitempty"`
	AutoModelIDs   json.RawMessage `json:"auto_model_ids,omitempty"`
}

// ErrGlobalConfigNotFound is returned by GetGlobalConfigEntry when no row
// matches the key. Callers treating absence as "use default" should use
// GetGlobalPolicy, which never returns this error.
var ErrGlobalConfigNotFound = errors.New("repository: global config entry not found")

// GetGlobalConfigEntry returns a single global_config row by key.
func (r *GormRepository) GetGlobalConfigEntry(ctx context.Context, key string) (GlobalConfig, error) {
	var row GlobalConfig
	if err := r.db.WithContext(ctx).Where("key = ?", key).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GlobalConfig{}, ErrGlobalConfigNotFound
		}
		return GlobalConfig{}, classifyReadErr(err)
	}
	return row, nil
}

// GetGlobalPolicy reads all three well-known global_config rows and returns
// their raw JSON values. Missing rows yield nil (caller applies defaults).
func (r *GormRepository) GetGlobalPolicy(ctx context.Context) (GlobalPolicy, error) {
	var rows []GlobalConfig
	if err := r.db.WithContext(ctx).Where("key IN ?", []string{
		string(GlobalKeyDefaultRetry), string(GlobalKeyDefaultTimeout), string(GlobalKeyAutoModelIDs),
	}).Find(&rows).Error; err != nil {
		return GlobalPolicy{}, classifyReadErr(err)
	}
	var p GlobalPolicy
	for _, row := range rows {
		switch GlobalConfigKey(row.Key) {
		case GlobalKeyDefaultRetry:
			p.DefaultRetry = json.RawMessage(row.Value)
		case GlobalKeyDefaultTimeout:
			p.DefaultTimeout = json.RawMessage(row.Value)
		case GlobalKeyAutoModelIDs:
			p.AutoModelIDs = json.RawMessage(row.Value)
		}
	}
	return p, nil
}

// SetGlobalConfigEntry upserts a single global_config row.
func (r *GormRepository) SetGlobalConfigEntry(ctx context.Context, key string, value []byte, updatedBy string) error {
	row := GlobalConfig{Key: key, Value: value, UpdatedBy: updatedBy}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.Assignments(map[string]any{"value": value, "updated_by": updatedBy, "updated_at": gorm.Expr("now()")}),
	}).Create(&row).Error; err != nil {
		return classifyWriteErr(err)
	}
	return nil
}
