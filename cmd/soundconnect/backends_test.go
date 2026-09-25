package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/config"
)

func TestBackendsJSONExposesSharedListenerAndCatalog(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runBackends([]string{"--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("runBackends() = %d, stderr = %q", code, stderr.String())
	}
	var response backendCatalogResponse
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
