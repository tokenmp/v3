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
	return nil, 0, nil
}
func (s *fakeStore) CreateAnnouncement(_ context.Context, a *models.Announcement) error { return nil }
func (s *fakeStore) UpdateAnnouncement(_ context.Context, id string, fields map[string]any) error {
	return nil
}
func (s *fakeStore) DeleteAnnouncement(_ context.Context, id string) error  { return nil }
func (s *fakeStore) PublishAnnouncement(_ context.Context, id string) error { return nil }
func (s *fakeStore) ListAllChangelogs(_ context.Context, limit, offset int) ([]models.Changelog, int, error) {
	return nil, 0, nil
}
func (s *fakeStore) CreateChangelog(_ context.Context, c *models.Changelog) error { return nil }
func (s *fakeStore) UpdateChangelog(_ context.Context, id string, fields map[string]any) error {
	return nil
}
func (s *fakeStore) DeleteChangelog(_ context.Context, id string) error  { return nil }
func (s *fakeStore) PublishChangelog(_ context.Context, id string) error { return nil }
func (s *fakeStore) ListAllNotifications(_ context.Context, limit, offset int) ([]models.Notification, int, error) {
	return nil, 0, nil
}
func (s *fakeStore) CreateNotification(_ context.Context, n *models.Notification) error { return nil }
func (s *fakeStore) DeleteNotification(_ context.Context, id string) error              { return nil }

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
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
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
	rec := doGet(s, "/api/v1/announcements", "")
	if rec.Code != 401 {
		t.Fatalf("got %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unauthorized") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestAnnouncements_OK(t *testing.T) {
	store := &fakeStore{announcements: []models.Announcement{
		{ID: "00000000-0000-0000-0000-000000000001", Title: "维护通知", Summary: "s", Body: "b", Severity: "maintenance", PublishedAt: time.Now()},
	}}
	s := newTestServer(t, store, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1"}})
	rec := doGet(s, "/api/v1/announcements?limit=5&offset=2", "tok")
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []models.Announcement `json:"items"`
		Total int                   `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
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
	rec := doGet(s, "/api/v1/announcements/not-a-uuid", "tok")
	if rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
	// valid uuid but not present -> 404
	rec = doGet(s, "/api/v1/announcements/00000000-0000-0000-0000-000000000099", "tok")
	if rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestChangelogs_OK(t *testing.T) {
	store := &fakeStore{changelogs: []models.Changelog{
		{ID: "00000000-0000-0000-0000-000000000002", Version: "v3.2.0", Title: "t", Body: "b", PublishedAt: time.Now()},
	}}
	s := newTestServer(t, store, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1"}})
	rec := doGet(s, "/api/v1/changelogs", "tok")
	if rec.Code != 200 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []models.Changelog `json:"items"`
		Total int                `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if page.Items[0].Version != "v3.2.0" {
		t.Errorf("version = %q", page.Items[0].Version)
	}
}

func TestNotifications_NullAction(t *testing.T) {
	store := &fakeStore{notifications: []models.Notification{
		{ID: "00000000-0000-0000-0000-000000000003", UserID: "u1", Type: "system", Title: "Welcome", Body: "hi"},
	}}
	s := newTestServer(t, store, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1"}})
	rec := doGet(s, "/api/v1/notifications", "tok")
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
	rec := doGet(s, "/api/v1/notifications", "tok")
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
	rec := doGet(s, "/api/v1/notifications/unread-count", "tok")
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if int(body["count"].(float64)) != 7 {
		t.Errorf("count = %v", body["count"])
	}
}

func TestMarkRead(t *testing.T) {
	store := &fakeStore{}
	s := newTestServer(t, store, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1"}})
	rec := doPost(s, "/api/v1/notifications/00000000-0000-0000-0000-000000000010/read", "tok")
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
	rec := doPost(s, "/api/v1/notifications/00000000-0000-0000-0000-000000000010/read", "tok")
	if rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestMarkAllRead(t *testing.T) {
	store := &fakeStore{}
	s := newTestServer(t, store, &fakeVerifier{subject: jwtverifier.Subject{UserID: "u1"}})
	rec := doPost(s, "/api/v1/notifications/read-all", "tok")
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
	rec := doGet(s, "/api/v1/announcements", "bad-token")
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
	rec = doGet(s, "/api/v1/announcements/00000000-0000-0000-0000-000000000099", "tok")
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("404 Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
}
