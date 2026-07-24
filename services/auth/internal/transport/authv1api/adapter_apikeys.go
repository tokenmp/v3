package authv1api

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/tokenmp/v3/services/auth/internal/contract/authv1"
	"github.com/tokenmp/v3/services/auth/internal/database/models"
	"github.com/tokenmp/v3/services/auth/internal/repository"
	"github.com/tokenmp/v3/services/auth/internal/security/apikey"
)

// ---------------------------------------------------------------------------
// APIKeyStore — 数据持久化端口
// ---------------------------------------------------------------------------

// APIKeyStore 是 API 密钥管理所需的持久化端口。transport 层只依赖此窄
// 接口，便于在测试中注入内存实现，且不直接依赖 repository 包的具体实现。
// 所有方法的错误均为 repository 分类 sentinel（ErrNotFound 等），不携带
// 驱动原始错误。
type APIKeyStore interface {
	// Create 插入一条新 API 密钥行。调用方需先用 apikey.Generate 生成完整
	// 密钥与哈希；本方法不返回任何密钥材料。
	Create(ctx context.Context, key *models.APIKey) error
	// ListByUser 返回该用户全部未吊销的密钥（最新优先），已禁用密钥仍可见。
	ListByUser(ctx context.Context, userID string) ([]models.APIKey, error)
	// FindByIDForUser 按主键加载用户拥有的非已吊销密钥；已吊销视为未找到。
	FindByIDForUser(ctx context.Context, id, userID string) (*models.APIKey, error)
	// UpdateFields 更新可变列（name 和/或 status）。缺失或已吊销返回 ErrNotFound。
	UpdateFields(ctx context.Context, id, userID string, fields map[string]any) error
	// Rotate 替换密钥的哈希/前缀/后缀并重新激活。缺失或已吊销返回 ErrNotFound。
	Rotate(ctx context.Context, id, userID string, hash []byte, prefix, suffix string) error
}

// APIKeyRepoAdapter 将 *repository.APIKeyRepository 适配为 APIKeyStore。
// 具体实现已满足接口，本适配器仅作为显式桥接与文档点；未来如需注入其它
// 实现（如缓存或 mock）可替换此处。
type APIKeyRepoAdapter struct {
	repo *repository.APIKeyRepository
}

// NewAPIKeyRepoAdapter 基于 GORM 仓储构造一个 APIKeyStore 适配器。
func NewAPIKeyRepoAdapter(repo *repository.APIKeyRepository) *APIKeyRepoAdapter {
	return &APIKeyRepoAdapter{repo: repo}
}

// Create 实现 APIKeyStore。
func (a *APIKeyRepoAdapter) Create(ctx context.Context, key *models.APIKey) error {
	return a.repo.Create(ctx, key)
}

// ListByUser 实现 APIKeyStore。
func (a *APIKeyRepoAdapter) ListByUser(ctx context.Context, userID string) ([]models.APIKey, error) {
	return a.repo.ListByUser(ctx, userID)
}

// FindByIDForUser 实现 APIKeyStore。
func (a *APIKeyRepoAdapter) FindByIDForUser(ctx context.Context, id, userID string) (*models.APIKey, error) {
	return a.repo.FindByIDForUser(ctx, id, userID)
}

// UpdateFields 实现 APIKeyStore。
func (a *APIKeyRepoAdapter) UpdateFields(ctx context.Context, id, userID string, fields map[string]any) error {
	return a.repo.UpdateFields(ctx, id, userID, fields)
}

// Rotate 实现 APIKeyStore。
func (a *APIKeyRepoAdapter) Rotate(ctx context.Context, id, userID string, hash []byte, prefix, suffix string) error {
	return a.repo.Rotate(ctx, id, userID, hash, prefix, suffix)
}

// ---------------------------------------------------------------------------
// 限制常量
// ---------------------------------------------------------------------------

// apiKeyMaxNameLength 限制密钥名称长度（与 migration VARCHAR(128) 对齐）。
const apiKeyMaxNameLength = 128

// ---------------------------------------------------------------------------
// StrictServerInterface —— API 密钥管理端点实现
// ---------------------------------------------------------------------------

// ListApiKeys 列出当前用户全部未吊销的 API 密钥。
func (a *StrictAdapter) AuthListApiKeys(ctx context.Context, _ authv1.AuthListApiKeysRequestObject) (authv1.AuthListApiKeysResponseObject, error) {
	userID := UserIDFromContext(ctx)
	if userID == "" {
		return authv1.AuthListApiKeys401JSONResponse{Body: errResp(authv1.Unauthorized, "authentication required"), Headers: authv1.AuthListApiKeys401ResponseHeaders(errHeaders())}, nil
	}
	if a.keys == nil {
		return authv1.AuthListApiKeys500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthListApiKeys500ResponseHeaders(errHeaders())}, nil
	}
	keys, err := a.keys.ListByUser(ctx, userID)
	if err != nil {
		return authv1.AuthListApiKeys500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthListApiKeys500ResponseHeaders(errHeaders())}, nil
	}
	out := make([]authv1.ApiKey, 0, len(keys))
	for i := range keys {
		out = append(out, apiKeyToGenerated(&keys[i]))
	}
	return authv1.AuthListApiKeys200JSONResponse{
		Body: struct {
			Keys []authv1.ApiKey `json:"keys"`
		}{Keys: out},
		Headers: authv1.AuthListApiKeys200ResponseHeaders{CacheControl: cacheControl(), ContentType: contentTypeJSON()},
	}, nil
}

// CreateApiKey 生成新密钥，仅返回一次性 secret。
func (a *StrictAdapter) AuthCreateApiKey(ctx context.Context, req authv1.AuthCreateApiKeyRequestObject) (authv1.AuthCreateApiKeyResponseObject, error) {
	userID := UserIDFromContext(ctx)
	if userID == "" {
		return authv1.AuthCreateApiKey401JSONResponse{Body: errResp(authv1.Unauthorized, "authentication required"), Headers: authv1.AuthCreateApiKey401ResponseHeaders(errHeaders())}, nil
	}
	if a.keys == nil {
		return authv1.AuthCreateApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthCreateApiKey500ResponseHeaders(errHeaders())}, nil
	}

	name := ""
	if req.Body != nil && req.Body.Name != nil {
		name = *req.Body.Name
	}
	if name == "" || len([]rune(name)) > apiKeyMaxNameLength {
		return authv1.AuthCreateApiKey400JSONResponse{Body: errResp(authv1.BadRequest, "name must be 1..128 characters"), Headers: authv1.AuthCreateApiKey400ResponseHeaders(errHeaders())}, nil
	}

	var expiresAt *time.Time
	if req.Body != nil && req.Body.ExpiresAt != nil {
		if req.Body.ExpiresAt.Before(time.Now().UTC()) {
			return authv1.AuthCreateApiKey400JSONResponse{Body: errResp(authv1.BadRequest, "expires_at must be in the future"), Headers: authv1.AuthCreateApiKey400ResponseHeaders(errHeaders())}, nil
		}
		t := *req.Body.ExpiresAt
		expiresAt = &t
	}

	fullKey, hash, err := apikey.Generate()
	if err != nil {
		return authv1.AuthCreateApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthCreateApiKey500ResponseHeaders(errHeaders())}, nil
	}

	key := &models.APIKey{
		UserID:    userID,
		Name:      name,
		KeyHash:   hash,
		KeyPrefix: apikey.Prefix(fullKey),
		KeySuffix: apikey.Suffix(fullKey),
		Role:      models.RoleUser,
		Status:    "active",
		ExpiresAt: expiresAt,
	}
	if err := a.keys.Create(ctx, key); err != nil {
		return authv1.AuthCreateApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthCreateApiKey500ResponseHeaders(errHeaders())}, nil
	}

	created := apiKeyCreatedToGenerated(key, fullKey)
	return authv1.AuthCreateApiKey201JSONResponse{
		Body: struct {
			Key authv1.ApiKeyCreated `json:"key"`
		}{Key: created},
		Headers: authv1.AuthCreateApiKey201ResponseHeaders{CacheControl: cacheControl(), ContentType: contentTypeJSON()},
	}, nil
}

// GetApiKey 返回单个密钥详情（不含 secret）。
func (a *StrictAdapter) AuthGetApiKey(ctx context.Context, req authv1.AuthGetApiKeyRequestObject) (authv1.AuthGetApiKeyResponseObject, error) {
	userID := UserIDFromContext(ctx)
	if userID == "" {
		return authv1.AuthGetApiKey401JSONResponse{Body: errResp(authv1.Unauthorized, "authentication required"), Headers: authv1.AuthGetApiKey401ResponseHeaders(errHeaders())}, nil
	}
	if a.keys == nil {
		return authv1.AuthGetApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthGetApiKey500ResponseHeaders(errHeaders())}, nil
	}
	key, err := a.keys.FindByIDForUser(ctx, req.KeyId.String(), userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return authv1.AuthGetApiKey404JSONResponse{Body: errResp(authv1.NotFound, "api key not found"), Headers: authv1.AuthGetApiKey404ResponseHeaders(errHeaders())}, nil
		}
		return authv1.AuthGetApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthGetApiKey500ResponseHeaders(errHeaders())}, nil
	}
	return authv1.AuthGetApiKey200JSONResponse{
		Body: struct {
			Key authv1.ApiKey `json:"key"`
		}{Key: apiKeyToGenerated(key)},
		Headers: authv1.AuthGetApiKey200ResponseHeaders{CacheControl: cacheControl(), ContentType: contentTypeJSON()},
	}, nil
}

// UpdateApiKey 更新密钥 name 和/或 status。
func (a *StrictAdapter) AuthUpdateApiKey(ctx context.Context, req authv1.AuthUpdateApiKeyRequestObject) (authv1.AuthUpdateApiKeyResponseObject, error) {
	userID := UserIDFromContext(ctx)
	if userID == "" {
		return authv1.AuthUpdateApiKey401JSONResponse{Body: errResp(authv1.Unauthorized, "authentication required"), Headers: authv1.AuthUpdateApiKey401ResponseHeaders(errHeaders())}, nil
	}
	if a.keys == nil {
		return authv1.AuthUpdateApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthUpdateApiKey500ResponseHeaders(errHeaders())}, nil
	}
	if req.Body == nil || (req.Body.Name == nil && req.Body.Status == nil) {
		return authv1.AuthUpdateApiKey400JSONResponse{Body: errResp(authv1.BadRequest, "must provide name or status"), Headers: authv1.AuthUpdateApiKey400ResponseHeaders(errHeaders())}, nil
	}

	fields := make(map[string]any)
	if req.Body.Name != nil {
		name := *req.Body.Name
		if name == "" || len([]rune(name)) > apiKeyMaxNameLength {
			return authv1.AuthUpdateApiKey400JSONResponse{Body: errResp(authv1.BadRequest, "name must be 1..128 characters"), Headers: authv1.AuthUpdateApiKey400ResponseHeaders(errHeaders())}, nil
		}
		fields["name"] = name
	}
	if req.Body.Status != nil {
		// 生成代码已校验枚举值（active|disabled），这里再显式确认以防绕过。
		status := string(*req.Body.Status)
		if status != "active" && status != "disabled" {
			return authv1.AuthUpdateApiKey400JSONResponse{Body: errResp(authv1.BadRequest, "status must be active or disabled"), Headers: authv1.AuthUpdateApiKey400ResponseHeaders(errHeaders())}, nil
		}
		fields["status"] = status
	}

	if err := a.keys.UpdateFields(ctx, req.KeyId.String(), userID, fields); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return authv1.AuthUpdateApiKey404JSONResponse{Body: errResp(authv1.NotFound, "api key not found"), Headers: authv1.AuthUpdateApiKey404ResponseHeaders(errHeaders())}, nil
		}
		return authv1.AuthUpdateApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthUpdateApiKey500ResponseHeaders(errHeaders())}, nil
	}

	key, err := a.keys.FindByIDForUser(ctx, req.KeyId.String(), userID)
	if err != nil {
		// 更新成功但回读失败：不暴露状态，返回 500。
		return authv1.AuthUpdateApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthUpdateApiKey500ResponseHeaders(errHeaders())}, nil
	}
	return authv1.AuthUpdateApiKey200JSONResponse{
		Body: struct {
			Key authv1.ApiKey `json:"key"`
		}{Key: apiKeyToGenerated(key)},
		Headers: authv1.AuthUpdateApiKey200ResponseHeaders{CacheControl: cacheControl(), ContentType: contentTypeJSON()},
	}, nil
}

// DeleteApiKey 软删除：将 status 置为 disabled。幂等：已禁用仍返回 204。
func (a *StrictAdapter) AuthDeleteApiKey(ctx context.Context, req authv1.AuthDeleteApiKeyRequestObject) (authv1.AuthDeleteApiKeyResponseObject, error) {
	userID := UserIDFromContext(ctx)
	if userID == "" {
		return authv1.AuthDeleteApiKey401JSONResponse{Body: errResp(authv1.Unauthorized, "authentication required"), Headers: authv1.AuthDeleteApiKey401ResponseHeaders(errHeaders())}, nil
	}
	if a.keys == nil {
		return authv1.AuthDeleteApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthDeleteApiKey500ResponseHeaders(errHeaders())}, nil
	}
	// 先确认密钥存在且属于该用户（已吊销→404）。
	if _, err := a.keys.FindByIDForUser(ctx, req.KeyId.String(), userID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return authv1.AuthDeleteApiKey404JSONResponse{Body: errResp(authv1.NotFound, "api key not found"), Headers: authv1.AuthDeleteApiKey404ResponseHeaders(errHeaders())}, nil
		}
		return authv1.AuthDeleteApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthDeleteApiKey500ResponseHeaders(errHeaders())}, nil
	}
	if err := a.keys.UpdateFields(ctx, req.KeyId.String(), userID, map[string]any{"status": "disabled"}); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return authv1.AuthDeleteApiKey404JSONResponse{Body: errResp(authv1.NotFound, "api key not found"), Headers: authv1.AuthDeleteApiKey404ResponseHeaders(errHeaders())}, nil
		}
		return authv1.AuthDeleteApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthDeleteApiKey500ResponseHeaders(errHeaders())}, nil
	}
	return authv1.AuthDeleteApiKey204Response{
		Headers: authv1.AuthDeleteApiKey204ResponseHeaders{CacheControl: cacheControl()},
	}, nil
}

// RotateApiKey 轮换密钥：生成新 secret，替换哈希，重新激活。
func (a *StrictAdapter) AuthRotateApiKey(ctx context.Context, req authv1.AuthRotateApiKeyRequestObject) (authv1.AuthRotateApiKeyResponseObject, error) {
	userID := UserIDFromContext(ctx)
	if userID == "" {
		return authv1.AuthRotateApiKey401JSONResponse{Body: errResp(authv1.Unauthorized, "authentication required"), Headers: authv1.AuthRotateApiKey401ResponseHeaders(errHeaders())}, nil
	}
	if a.keys == nil {
		return authv1.AuthRotateApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthRotateApiKey500ResponseHeaders(errHeaders())}, nil
	}

	fullKey, hash, err := apikey.Generate()
	if err != nil {
		return authv1.AuthRotateApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthRotateApiKey500ResponseHeaders(errHeaders())}, nil
	}
	if err := a.keys.Rotate(ctx, req.KeyId.String(), userID, hash, apikey.Prefix(fullKey), apikey.Suffix(fullKey)); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return authv1.AuthRotateApiKey404JSONResponse{Body: errResp(authv1.NotFound, "api key not found"), Headers: authv1.AuthRotateApiKey404ResponseHeaders(errHeaders())}, nil
		}
		return authv1.AuthRotateApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthRotateApiKey500ResponseHeaders(errHeaders())}, nil
	}

	key, err := a.keys.FindByIDForUser(ctx, req.KeyId.String(), userID)
	if err != nil {
		// 轮换成功但回读失败：新 secret 已无法再次返回，返回 500。
		return authv1.AuthRotateApiKey500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthRotateApiKey500ResponseHeaders(errHeaders())}, nil
	}
	created := apiKeyCreatedToGenerated(key, fullKey)
	return authv1.AuthRotateApiKey200JSONResponse{
		Body: struct {
			Key authv1.ApiKeyCreated `json:"key"`
		}{Key: created},
		Headers: authv1.AuthRotateApiKey200ResponseHeaders{CacheControl: cacheControl(), ContentType: contentTypeJSON()},
	}, nil
}

// ---------------------------------------------------------------------------
// 转换辅助
// ---------------------------------------------------------------------------

// apiKeyToGenerated 将持久化模型转换为对外 ApiKey（不含 secret）。
func apiKeyToGenerated(k *models.APIKey) authv1.ApiKey {
	id, _ := uuid.Parse(k.ID)
	return authv1.ApiKey{
		Id:         id,
		Name:       k.Name,
		KeyPrefix:  k.KeyPrefix,
		KeySuffix:  k.KeySuffix,
		Status:     authv1.ApiKeyStatus(k.Status),
		LastUsedAt: k.LastUsedAt,
		ExpiresAt:  k.ExpiresAt,
		CreatedAt:  k.CreatedAt,
	}
}

// apiKeyCreatedToGenerated 将模型 + 一次性完整密钥转换为 ApiKeyCreated。
func apiKeyCreatedToGenerated(k *models.APIKey, secret string) authv1.ApiKeyCreated {
	id, _ := uuid.Parse(k.ID)
	return authv1.ApiKeyCreated{
		Id:        id,
		Name:      k.Name,
		KeyPrefix: k.KeyPrefix,
		KeySuffix: k.KeySuffix,
		Secret:    secret,
		Status:    authv1.ApiKeyCreatedStatusActive,
		CreatedAt: k.CreatedAt,
	}
}
