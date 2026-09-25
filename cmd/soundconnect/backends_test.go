package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/config"
)

func TestBackendsJSONExposesSharedListenerAndCatalog(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"backends", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(backends) = %d, stderr = %q", code, stderr.String())
	}
	var response app.BackendCatalog
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != 1 || response.SOCKSListen != config.DefaultSOCKSListen {
		t.Fatalf("response = %#v", response)
	}
	if len(response.Backends) != 2 || response.Backends[1].ID != backend.ATrust {
		t.Fatalf("backends = %#v", response.Backends)
	}
}
