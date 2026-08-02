package traffic

import (
	"testing"
	"time"
)

func TestCountersTrackPayloadAndConnections(t *testing.T) {
	startedAt := time.Date(2026, time.July, 12, 8, 30, 0, 0, time.UTC)
	var counters Counters
	counters.BeginSession(startedAt)
	counters.ConnectionOpened()
	counters.AddUpload(1024)
	counters.AddDownload(4096)

	snapshot := counters.Snapshot()
	if !snapshot.SessionStartedAt.Equal(startedAt) {
		t.Fatalf("session start = %s", snapshot.SessionStartedAt)
	}
	if snapshot.UploadBytes != 1024 || snapshot.DownloadBytes != 4096 {
		t.Fatalf("traffic snapshot = %+v", snapshot)
	}
	if snapshot.ActiveConnections != 1 || snapshot.TotalConnections != 1 {
		t.Fatalf("connection snapshot = %+v", snapshot)
	}

	counters.ConnectionClosed()
	if active := counters.Snapshot().ActiveConnections; active != 0 {
		t.Fatalf("active connections = %d", active)
	}
}
