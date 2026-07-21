package executorv1api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

type strictTrackingWriter struct {
	header http.Header
	writes int
}

func (w *strictTrackingWriter) Header() http.Header { return w.header }
func (w *strictTrackingWriter) WriteHeader(int)     { w.writes++ }
func (w *strictTrackingWriter) Write(p []byte) (int, error) {
	w.writes++
	return len(p), nil
}

func TestSafeStrictOptionsContextLifecycleDoesNotWrite(t *testing.T) {
	t.Parallel()
	options := SafeStrictOptions()
	for _, handler := range []func(http.ResponseWriter, *http.Request, error){
		options.RequestErrorHandlerFunc,
		options.ResponseErrorHandlerFunc,
	} {
		for _, err := range []error{
			context.Canceled,
			context.DeadlineExceeded,
			fmt.Errorf("wrapped: %w", context.Canceled),
			fmt.Errorf("wrapped: %w", context.DeadlineExceeded),
		} {
			writer := &strictTrackingWriter{header: make(http.Header)}
			handler(writer, newRequest(http.MethodPost, openAIChatPath, ""), err)
			if writer.writes != 0 || len(writer.header) != 0 {
				t.Fatalf("handler wrote lifecycle error %v: writes=%d headers=%#v", err, writer.writes, writer.header)
			}
		}
	}
}

func TestSafeStrictOptionsDoesNotExposeOtherErrors(t *testing.T) {
	t.Parallel()
	writer := &strictTrackingWriter{header: make(http.Header)}
	SafeStrictOptions().ResponseErrorHandlerFunc(writer, newRequest(http.MethodPost, openAIChatPath, ""), errors.New("private detail"))
	if writer.writes == 0 {
		t.Fatal("non-lifecycle error was not rendered")
	}
}
