package envelope

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	})
}

func errHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func TestWrap_Success(t *testing.T) {
	rec := httptest.NewRecorder()
	Wrap(okHandler(`{"id":"abc","name":"test"}`)).ServeHTTP(rec, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var env struct {
		Code    int             `json:"code"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if env.Code != 0 {
		t.Errorf("code = %d, want 0", env.Code)
	}
	if env.Message != "success" {
		t.Errorf("message = %q, want 'success'", env.Message)
	}
	// data should be the original body
	var d struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(env.Data, &d); err != nil {
		t.Fatalf("decode data: %v (data=%s)", err, string(env.Data))
	}
	if d.ID != "abc" || d.Name != "test" {
		t.Errorf("data = %+v", d)
	}
}

func TestWrap_Error(t *testing.T) {
	rec := httptest.NewRecorder()
	Wrap(errHandler(http.StatusUnauthorized, `{"error":{"code":"invalid_credentials","message":"wrong password"}}`)).ServeHTTP(rec, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var env struct {
		Code    int         `json:"code"`
		Data    interface{} `json:"data"`
		Message string      `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if env.Code != 1007 {
		t.Errorf("code = %d, want 1007", env.Code)
	}
	if env.Data != nil {
		t.Errorf("data = %v, want nil", env.Data)
	}
	if env.Message != "wrong password" {
		t.Errorf("message = %q, want 'wrong password'", env.Message)
	}
}

func TestWrap_204(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	Wrap(handler).ServeHTTP(rec, nil)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

func TestWrap_NonJSON(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	rec := httptest.NewRecorder()
	Wrap(handler).ServeHTTP(rec, nil)

	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want 'ok'", rec.Body.String())
	}
}

func TestWrap_Paginated(t *testing.T) {
	rec := httptest.NewRecorder()
	Wrap(okHandler(`{"items":[{"id":"a"},{"id":"b"}],"total":2,"page":1,"pageSize":20}`)).ServeHTTP(rec, nil)

	var env struct {
		Code    int             `json:"code"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)

	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total    int `json:"total"`
		Page     int `json:"page"`
		PageSize int `json:"pageSize"`
	}
	if err := json.Unmarshal(env.Data, &page); err != nil {
		t.Fatalf("decode data: %v (data=%s)", err, string(env.Data))
	}
	if len(page.Items) != 2 || page.Total != 2 || page.PageSize != 20 {
		t.Errorf("page = %+v", page)
	}
}
