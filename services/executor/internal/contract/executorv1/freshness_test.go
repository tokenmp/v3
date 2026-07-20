// Package executorv1 verifies committed generated-file source markers without
// running the generator, so ordinary go test remains fast and offline.
package executorv1

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGeneratedFilesHaveMarkers(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	for _, name := range []string{"models.gen.go", "server.gen.go"} {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		content := string(data)
		if !strings.Contains(content, "from packages/contracts/openapi/executor/v1.yaml") {
			t.Errorf("%s missing source marker", name)
		}
		if !strings.Contains(content, "DO NOT EDIT") {
			t.Errorf("%s missing DO NOT EDIT marker", name)
		}
	}
}
