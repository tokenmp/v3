package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/notice/internal/jwtverifier"
	"github.com/tokenmp/v3/services/notice/internal/models"
	"github.com/tokenmp/v3/services/notice/internal/repository"
)

// decodeData extracts the .data field from the {code,data,message} envelope.
func decodeData(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	var env struct {
		Code    int             `json:"code"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, rec.Body.String())
	}
	if env.Code != 0 {
		t.Fatalf("envelope code = %d, want 0 (body=%s)", env.Code, rec.Body.String())
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return
	}
	if err := json.Unmarshal(env.Data, v); err != nil {
		t.Fatalf("decode data: %v (data=%s)", err, string(env.Data))
	}
}

// --- fakes ---

type fakeVerifier struct {
	subject jwtverifier.Subject
	err     error
	called  bool
	token   string
}

func (f *fakeVerifier) Verify(raw string) (jwtverifier.Subject, error) {
	f.called = true
	f.token = raw
	if f.err != nil {
		return jwtverifier.Subject{}, f.err
	}
	return f.subject, nil
}

type fakeStore struct {
	announcements    []models.Announcement
	announcementErr  map[string]error
	changelogs       []models.Changelog
	changelogErr     map[string]error
	notifications    []models.Notification
	notificationsErr error
	unreadCount      int
	markReadErr      error
	markAllReadErr   error
	listLimit        int
	listOffset       int
	markedRead       []string
	markedAll        bool

	// Admin state
	adminAnnouncements  []models.Announcement
	adminChangelogs     []models.Changelog
	adminNotifications  []models.Notification
	createdAnnouncement *models.Announcement
	createdChangelog    *models.Changelog
	createdNotification *models.Notification
	updatedAnnFields    map[string]any
	updatedClFields     map[string]any
}

func (s *fakeStore) ListAnnouncements(ctx context.Context, limit, offset int) ([]models.Announcement, int, error) {
	s.listLimit, s.listOffset = limit, offset
	return s.announcements, len(s.announcements), nil
}

func (s *fakeStore) GetAnnouncement(ctx context.Context, id string) (models.Announcement, error) {
	if err, ok := s.announcementErr[id]; ok {
		return models.Announcement{}, err
	}
	for _, a := range s.announcements {
		if a.ID == id {
			return a, nil
		}
	}
	return models.Announcement{}, repository.ErrNotFound
}

func (s *fakeStore) ListChangelogs(ctx context.Context, limit, offset int) ([]models.Changelog, int, error) {
	return s.changelogs, len(s.changelogs), nil
}

func (s *fakeStore) GetChangelog(ctx context.Context, id string) (models.Changelog, error) {
	if err, ok := s.changelogErr[id]; ok {
		return models.Changelog{}, err
	}
	for _, c := range s.changelogs {
		if c.ID == id {
			return c, nil
		}
	}
	return models.Changelog{}, repository.ErrNotFound
}

func (s *fakeStore) ListNotifications(ctx context.Context, userID string, limit, offset int) ([]models.Notification, int, error) {
	if s.notificationsErr != nil {
		return nil, 0, s.notificationsErr
	}
	return s.notifications, len(s.notifications), nil
}

func (s *fakeStore) UnreadCount(ctx context.Context, userID string) (int, error) {
	return s.unreadCount, nil
}

func (s *fakeStore) MarkRead(ctx context.Context, userID, id string) error {
	if s.markReadErr != nil {
		return s.markReadErr
	}
	s.markedRead = append(s.markedRead, id)
	return nil
}

func (s *fakeStore) MarkAllRead(ctx context.Context, userID string) error {
	if s.markAllReadErr != nil {
		return s.markAllReadErr
	}
	s.markedAll = true
	return nil
}

// Admin stubs for fakeStore.

func (s *fakeStore) ListAllAnnouncements(_ context.Context, limit, offset int) ([]models.Announcement, int, error) {
	return s.adminAnnouncements, len(s.adminAnnouncements), nil
}
func (s *fakeStore) CreateAnnouncement(_ context.Context, a *models.Announcement) error {
	s.createdAnnouncement = a
	return nil
}
func (s *fakeStore) UpdateAnnouncement(_ context.Context, id string, fields map[string]any) error {
	s.updatedAnnFields = fields
	return nil
}
func (s *fakeStore) DeleteAnnouncement(_ context.Context, id string) error  { return nil }
func (s *fakeStore) PublishAnnouncement(_ context.Context, id string) error { return nil }
func (s *fakeStore) ListAllChangelogs(_ context.Context, limit, offset int) ([]models.Changelog, int, error) {
	return s.adminChangelogs, len(s.adminChangelogs), nil
}
func (s *fakeStore) CreateChangelog(_ context.Context, c *models.Changelog) error {
	s.createdChangelog = c
	return nil
}
func (s *fakeStore) UpdateChangelog(_ context.Context, id string, fields map[string]any) error {
	s.updatedClFields = fields
	return nil
}
func (s *fakeStore) DeleteChangelog(_ context.Context, id string) error  { return nil }
func (s *fakeStore) PublishChangelog(_ context.Context, id string) error { return nil }
func (s *fakeStore) ListAllNotifications(_ context.Context, limit, offset int) ([]models.Notification, int, error) {
	return s.adminNotifications, len(s.adminNotifications), nil
}
func (s *fakeStore) CreateNotification(_ context.Context, n *models.Notification) error {
	s.createdNotification = n
	return nil
}
func (s *fakeStore) DeleteNotification(_ context.Context, id string) error { return nil }

// repository.ErrNotFound is used directly by the fakes below.

// --- helpers ---

func newTestServer(t *testing.T, store Store, verifier AuthVerifier) *http.Server {
	t.Helper()
	return New(ServerConfig{
		Addr:     "",
		Pinger:   &okPinger{},
		Verifier: verifier,
		Store:    store,
		Logger:   nil,
	})
}

type okPinger struct{}

func (okPinger) Ping(context.Context) error { return nil }

func doGet(s *http.Server, target string, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)
	return rec
}

func doPost(s *http.Server, target string, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)
	return rec
}

// --- tests ---

func TestHealthzAndReadyz(t *testing.T) {
	s := newTestServer(t, &fakeStore{}, &fakeVerifier{subject: jwtverifier.Subject{}})

	rec := doGet(s, "/healthz", "")
	if rec.Code != 200 {
		t.Fatalf("healthz: %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	var body map[string]any
	decodeData(t, rec, &body)
	if body["service"] != "notice" || body["status"] != "ok" {
		t.Errorf("healthz body = %v", body)
	}

	rec = doGet(s, "/readyz", "")
	if rec.Code != 200 {
		t.Fatalf("readyz: %d", rec.Code)
	}
}

func TestAnnouncements_Unauth(t *testing.T) {
	s := newTestServer(t, &fakeStore{}, &fakeVerifier{})
	rec := doGet(s, "/api/v1/notice/announcements", "")
	if rec.Code != 401 {
		t.Fatalf("got %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "1007") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestAnnouncements_OK(t *testing.T) {
	store := &fakeStore{announcements: []models.Announcement{
		{ID: "00000000-0000-0000-0000-000000000001", Title: "维护通知", Summary: "s", Body: "b", Severity: "maintenance", PublishedAt: time.Now()},
	}}
	s := newTestServer(t, store, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1"}})
	rec := doGet(s, "/api/v1/notice/announcements?limit=5&offset=2", "tok")
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []models.Announcement `json:"items"`
		Total int                   `json:"total"`
	}
	decodeData(t, rec, &page)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("page = %+v", page)
	}
	if store.listLimit != 5 || store.listOffset != 2 {
		t.Errorf("paging forwarded as limit=%d offset=%d", store.listLimit, store.listOffset)
	}
}

func TestGetAnnouncement_NotFound(t *testing.T) {
	store := &fakeStore{}
	s := newTestServer(t, store, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1"}})
	// invalid uuid shape -> 404
	rec := doGet(s, "/api/v1/notice/announcements/not-a-uuid", "tok")
	if rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
	// valid uuid but not present -> 404
	rec = doGet(s, "/api/v1/notice/announcements/00000000-0000-0000-0000-000000000099", "tok")
	if rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestChangelogs_OK(t *testing.T) {
	store := &fakeStore{changelogs: []models.Changelog{
		{ID: "00000000-0000-0000-0000-000000000002", Version: "v3.2.0", Title: "t", Body: "b", PublishedAt: time.Now()},
	}}
	s := newTestServer(t, store, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1"}})
	rec := doGet(s, "/api/v1/notice/changelogs", "tok")
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []models.Changelog `json:"items"`
		Total int                `json:"total"`
	}
	decodeData(t, rec, &page)
	if page.Items[0].Version != "v3.2.0" {
		t.Errorf("version = %q", page.Items[0].Version)
	}
}

func TestNotifications_NullAction(t *testing.T) {
	store := &fakeStore{notifications: []models.Notification{
		{ID: "00000000-0000-0000-0000-000000000003", UserID: "u1", Type: "system", Title: "Welcome", Body: "hi"},
	}}
	s := newTestServer(t, store, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1"}})
	rec := doGet(s, "/api/v1/notice/notifications", "tok")
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"action":null`) {
		t.Errorf("expected null action, body=%s", body)
	}
}

func TestNotifications_WithAction(t *testing.T) {
	act := models.NotificationAction{Type: "link", Label: "查看套餐", Href: "/panel/billing/plans/plan_basic"}
	store := &fakeStore{notifications: []models.Notification{
		{ID: "00000000-0000-0000-0000-000000000004", UserID: "u1", Type: "plan_activated", Title: "套餐已启用", Body: "您的套餐已启用", Action: models.NotificationActionPtr{Action: &act}},
	}}
	s := newTestServer(t, store, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1"}})
	rec := doGet(s, "/api/v1/notice/notifications", "tok")
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"href":"/panel/billing/plans/plan_basic"`) {
		t.Errorf("missing href in body: %s", body)
	}
	if !strings.Contains(body, `"label":"查看套餐"`) {
		t.Errorf("missing label: %s", body)
	}
}

func TestUnreadCount(t *testing.T) {
	store := &fakeStore{unreadCount: 7}
	s := newTestServer(t, store, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1"}})
	rec := doGet(s, "/api/v1/notice/notifications/unread-count", "tok")
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	var body map[string]any
	decodeData(t, rec, &body)
	if int(body["count"].(float64)) != 7 {
		t.Errorf("count = %v", body["count"])
	}
}

func TestMarkRead(t *testing.T) {
	store := &fakeStore{}
	s := newTestServer(t, store, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1"}})
	rec := doPost(s, "/api/v1/notice/notifications/00000000-0000-0000-0000-000000000010/read", "tok")
	if rec.Code != 204 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if len(store.markedRead) != 1 || store.markedRead[0] != "00000000-0000-0000-0000-000000000010" {
		t.Errorf("markedRead = %v", store.markedRead)
	}
}

func TestMarkRead_NotFound(t *testing.T) {
	store := &fakeStore{markReadErr: repository.ErrNotFound}
	s := newTestServer(t, store, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1"}})
	rec := doPost(s, "/api/v1/notice/notifications/00000000-0000-0000-0000-000000000010/read", "tok")
	if rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestMarkAllRead(t *testing.T) {
	store := &fakeStore{}
	s := newTestServer(t, store, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1"}})
	rec := doPost(s, "/api/v1/notice/notifications/read-all", "tok")
	if rec.Code != 204 {
		t.Fatalf("got %d", rec.Code)
	}
	if !store.markedAll {
		t.Error("MarkAllRead not invoked")
	}
}

func TestInvalidBearer(t *testing.T) {
	store := &fakeStore{}
	s := newTestServer(t, store, &fakeVerifier{err: jwtverifier.ErrInvalidToken})
	rec := doGet(s, "/api/v1/notice/announcements", "bad-token")
	if rec.Code != 401 {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}

func TestNoStoreOnAllResponses(t *testing.T) {
	s := newTestServer(t, &fakeStore{}, &fakeVerifier{})
	rec := doGet(s, "/healthz", "")
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("healthz Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	// even 404 responses
	rec = doGet(s, "/api/v1/notice/announcements/00000000-0000-0000-0000-000000000099", "tok")
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("404 Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
}

// --- Admin endpoint tests ---

func doAdminGet(s *http.Server, target string) *httptest.ResponseRecorder {
	return doGet(s, target, "admin-token")
}

func doAdminPost(s *http.Server, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer admin-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)
	return rec
}

func doAdminPatch(s *http.Server, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer admin-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)
	return rec
}

func adminVerifier() *fakeVerifier {
	return &fakeVerifier{subject: jwtverifier.Subject{UserID: "admin1", Role: "admin"}}
}

func TestAdminListAnnouncements_ExposeTimestamps(t *testing.T) {
	now := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	pub := time.Date(2025, 1, 14, 0, 0, 0, 0, time.UTC)
	store := &fakeStore{adminAnnouncements: []models.Announcement{
		{ID: "a1", Title: "T", Summary: "S", Body: "B", Severity: "info", PublishedAt: pub, CreatedAt: now, UpdatedAt: now},
	}}
	s := newTestServer(t, store, adminVerifier())
	rec := doAdminGet(s, "/api/v1/notice/admin/announcements")
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// created_at and updated_at must appear (models have json:"-")
	if !strings.Contains(body, `"created_at"`) {
		t.Errorf("missing created_at in response: %s", body)
	}
	if !strings.Contains(body, `"updated_at"`) {
		t.Errorf("missing updated_at in response: %s", body)
	}
	if !strings.Contains(body, `"published_at"`) {
		t.Errorf("missing published_at in response: %s", body)
	}
}

func TestAdminListAnnouncements_DraftNullPublishedAt(t *testing.T) {
	now := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	store := &fakeStore{adminAnnouncements: []models.Announcement{
		{ID: "a2", Title: "Draft", Summary: "", Body: "", Severity: "info", CreatedAt: now, UpdatedAt: now},
	}}
	s := newTestServer(t, store, adminVerifier())
	rec := doAdminGet(s, "/api/v1/notice/admin/announcements")
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"published_at":null`) {
		t.Errorf("draft should have null published_at, body=%s", body)
	}
}

func TestAdminCreateAnnouncement_WithPublishedAt(t *testing.T) {
	store := &fakeStore{}
	s := newTestServer(t, store, adminVerifier())
	pub := "2025-06-01T00:00:00Z"
	rec := doAdminPost(s, "/api/v1/notice/admin/announcements", `{"title":"T","summary":"S","body":"B","severity":"warning","published_at":"`+pub+`"}`)
	if rec.Code != 201 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if store.createdAnnouncement == nil {
		t.Fatal("announcement not created")
	}
	if store.createdAnnouncement.PublishedAt.IsZero() {
		t.Error("published_at should be set on model")
	}
	// Response should contain published_at
	body := rec.Body.String()
	if !strings.Contains(body, `"published_at"`) {
		t.Errorf("response missing published_at: %s", body)
	}
}

func TestAdminCreateAnnouncement_DraftNoPublishedAt(t *testing.T) {
	store := &fakeStore{}
	s := newTestServer(t, store, adminVerifier())
	rec := doAdminPost(s, "/api/v1/notice/admin/announcements", `{"title":"Draft","summary":"","body":"","severity":"info"}`)
	if rec.Code != 201 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if store.createdAnnouncement == nil {
		t.Fatal("announcement not created")
	}
	// When the caller omits published_at, the server defaults it to now
	// (rather than inserting a 0001-01-01 zero value that overrides the DB
	// DEFAULT and confuses downstream rendering/sorting).
	if store.createdAnnouncement.PublishedAt.IsZero() {
		t.Error("published_at should default to now, got zero value")
	}
}

func TestAdminUpdateAnnouncement_AllowPublishedAt(t *testing.T) {
	store := &fakeStore{}
	s := newTestServer(t, store, adminVerifier())
	rec := doAdminPatch(s, "/api/v1/notice/admin/announcements/a1", `{"title":"New","published_at":"2025-07-01T00:00:00Z"}`)
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if store.updatedAnnFields == nil {
		t.Fatal("no fields passed to UpdateAnnouncement")
	}
	if _, ok := store.updatedAnnFields["published_at"]; !ok {
		t.Errorf("published_at not in allowed fields: %v", store.updatedAnnFields)
	}
	if _, ok := store.updatedAnnFields["title"]; !ok {
		t.Errorf("title not in allowed fields: %v", store.updatedAnnFields)
	}
}

func TestAdminListChangelogs_ExposeTimestamps(t *testing.T) {
	now := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	pub := time.Date(2025, 2, 28, 0, 0, 0, 0, time.UTC)
	store := &fakeStore{adminChangelogs: []models.Changelog{
		{ID: "c1", Version: "v3.0.0", Title: "Release", Body: "notes", PublishedAt: pub, CreatedAt: now, UpdatedAt: now},
	}}
	s := newTestServer(t, store, adminVerifier())
	rec := doAdminGet(s, "/api/v1/notice/admin/changelogs")
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"created_at"`) {
		t.Errorf("missing created_at: %s", body)
	}
	if !strings.Contains(body, `"updated_at"`) {
		t.Errorf("missing updated_at: %s", body)
	}
	if !strings.Contains(body, `"published_at"`) {
		t.Errorf("missing published_at: %s", body)
	}
}

func TestAdminCreateChangelog_WithPublishedAt(t *testing.T) {
	store := &fakeStore{}
	s := newTestServer(t, store, adminVerifier())
	rec := doAdminPost(s, "/api/v1/notice/admin/changelogs", `{"version":"v1.0.0","title":"Release","body":"notes","published_at":"2025-06-01T00:00:00Z"}`)
	if rec.Code != 201 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if store.createdChangelog == nil {
		t.Fatal("changelog not created")
	}
	if store.createdChangelog.PublishedAt.IsZero() {
		t.Error("published_at should be set on model")
	}
}

func TestAdminUpdateChangelog_AllowPublishedAt(t *testing.T) {
	store := &fakeStore{}
	s := newTestServer(t, store, adminVerifier())
	rec := doAdminPatch(s, "/api/v1/notice/admin/changelogs/c1", `{"version":"v2.0.0","published_at":"2025-08-01T00:00:00Z"}`)
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if store.updatedClFields == nil {
		t.Fatal("no fields passed to UpdateChangelog")
	}
	if _, ok := store.updatedClFields["published_at"]; !ok {
		t.Errorf("published_at not in allowed fields: %v", store.updatedClFields)
	}
}

func TestAdminListNotifications_ExposeUserIDAndAction(t *testing.T) {
	now := time.Date(2025, 5, 1, 8, 0, 0, 0, time.UTC)
	act := models.NotificationAction{Type: "link", Label: "View", Href: "/settings"}
	store := &fakeStore{adminNotifications: []models.Notification{
		{ID: "n1", UserID: "user-42", Type: "system", Title: "Hi", Body: "msg", Action: models.NotificationActionPtr{Action: &act}, CreatedAt: now},
		{ID: "n2", UserID: "user-99", Type: "info", Title: "Info", Body: "note", CreatedAt: now},
	}}
	s := newTestServer(t, store, adminVerifier())
	rec := doAdminGet(s, "/api/v1/notice/admin/notifications")
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// user_id must appear (model has json:"-")
	if !strings.Contains(body, `"user_id":"user-42"`) {
		t.Errorf("missing user_id in response: %s", body)
	}
	if !strings.Contains(body, `"user_id":"user-99"`) {
		t.Errorf("missing second user_id: %s", body)
	}
	// action with data
	if !strings.Contains(body, `"label":"View"`) {
		t.Errorf("missing action label: %s", body)
	}
	// null action for second
	if !strings.Contains(body, `"action":null`) {
		t.Errorf("missing null action: %s", body)
	}
}

func TestAdminSendNotification_ResponseShape(t *testing.T) {
	store := &fakeStore{}
	s := newTestServer(t, store, adminVerifier())
	rec := doAdminPost(s, "/api/v1/notice/admin/notifications/send", `{"userId":"u1","type":"info","title":"T","body":"B"}`)
	if rec.Code != 202 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"accepted":true`) {
		t.Errorf("missing accepted in response: %s", body)
	}
	if !strings.Contains(body, `"queuedAt"`) {
		t.Errorf("missing queuedAt in response: %s", body)
	}
}

func TestAdminSendNotification_BroadcastUsesSentinel(t *testing.T) {
	store := &fakeStore{}
	s := newTestServer(t, store, adminVerifier())
	rec := doAdminPost(s, "/api/v1/notice/admin/notifications/send", `{"type":"system","title":"T","body":"B"}`)
	if rec.Code != 202 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if store.createdNotification == nil {
		t.Fatal("notification was not created")
	}
	if store.createdNotification.UserID != models.BroadcastUserID {
		t.Fatalf("userID=%q want broadcast sentinel", store.createdNotification.UserID)
	}
}

func TestAdminForbidden_NonAdmin(t *testing.T) {
	s := newTestServer(t, &fakeStore{}, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1", Role: "user"}})
	rec := doGet(s, "/api/v1/notice/admin/announcements", "user-token")
	if rec.Code != 403 {
		t.Fatalf("got %d, want 403", rec.Code)
	}
}
