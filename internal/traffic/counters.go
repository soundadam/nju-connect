// Package traffic records application payload at the local ingress boundary.
package traffic

import (
	"sync/atomic"
	"time"
)

type Snapshot struct {
	SessionStartedAt  time.Time
	UploadBytes       uint64
	DownloadBytes     uint64
	ActiveConnections int64
	TotalConnections  uint64
}

type Counters struct {
	sessionStartedAt  atomic.Int64
	uploadBytes       atomic.Uint64
	downloadBytes     atomic.Uint64
	activeConnections atomic.Int64
	totalConnections  atomic.Uint64
}

func (counters *Counters) BeginSession(startedAt time.Time) {
	if counters == nil {
		return
	}
	counters.uploadBytes.Store(0)
	counters.downloadBytes.Store(0)
	counters.activeConnections.Store(0)
	counters.totalConnections.Store(0)
	counters.sessionStartedAt.Store(startedAt.UTC().UnixNano())
}

func (counters *Counters) AddUpload(bytes uint64) {
	if counters != nil && bytes > 0 {
		counters.uploadBytes.Add(bytes)
	}
}

func (counters *Counters) AddDownload(bytes uint64) {
	if counters != nil && bytes > 0 {
		counters.downloadBytes.Add(bytes)
	}
}

func (counters *Counters) ConnectionOpened() {
	if counters == nil {
		return
	}
	counters.activeConnections.Add(1)
	counters.totalConnections.Add(1)
}

func (counters *Counters) ConnectionClosed() {
	if counters != nil {
		counters.activeConnections.Add(-1)
	}
}

func (counters *Counters) Snapshot() Snapshot {
	if counters == nil {
		return Snapshot{}
	}
	startedAt := counters.sessionStartedAt.Load()
	snapshot := Snapshot{
		UploadBytes:       counters.uploadBytes.Load(),
		DownloadBytes:     counters.downloadBytes.Load(),
		ActiveConnections: counters.activeConnections.Load(),
		TotalConnections:  counters.totalConnections.Load(),
	}
	if startedAt != 0 {
		snapshot.SessionStartedAt = time.Unix(0, startedAt).UTC()
	}
	return snapshot
}
