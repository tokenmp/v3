// Package healthz provides the Executor health endpoint.
package healthz

import "net/http"

// NewHandler returns the HTTP handler for GET and HEAD /healthz requests.
func NewHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" {
			http.NotFound(writer, request)
			return
		}

		switch request.Method {
		case http.MethodGet, http.MethodHead:
			writer.Header().Set("Content-Type", "application/json")
			writer.Header().Set("Cache-Control", "no-store")
			writer.WriteHeader(http.StatusOK)
			if request.Method == http.MethodGet {
				_, _ = writer.Write([]byte("{\"status\":\"ok\"}\n"))
			}
		default:
			writer.Header().Set("Allow", "GET, HEAD")
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}
