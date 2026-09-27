package speedtest

import (
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	SchemaVersion = 1
	TargetHost    = "speed.nju.edu.cn"
	TargetURL     = "http://speed.nju.edu.cn"
	HelperName    = "librespeed-cli"
	HelperVersion = "v1.0.13-campus.1"
)

type Route string

const (
	RouteAuto       Route = "auto"
	RouteDirect     Route = "direct"
	RouteNJUConnect Route = "nju-connect"
)

func ParseRoute(value string) (Route, error) {
	route := Route(value)
	switch route {
	case RouteAuto, RouteDirect, RouteNJUConnect:
		return route, nil
	default:
		return "", fmt.Errorf("unsupported speed-test route %q", value)
	}
}

type Status string

const (
	StatusSuccess   Status = "success"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type Failure struct {
	Stage   string `json:"stage"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Result struct {
	SchemaVersion int       `json:"schema_version"`
	Status        Status    `json:"status"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
	Target        string    `json:"target"`
	Family        string    `json:"family"`
	Route         Route     `json:"route"`
	Server        string    `json:"server,omitempty"`
	PingMS        *float64  `json:"ping_ms,omitempty"`
	JitterMS      *float64  `json:"jitter_ms,omitempty"`
	DownloadMbps  *float64  `json:"download_mbps,omitempty"`
	UploadMbps    *float64  `json:"upload_mbps,omitempty"`
	HelperVersion string    `json:"helper_version,omitempty"`
	Failure       *Failure  `json:"failure,omitempty"`
}

type ProbeResult struct {
	SchemaVersion int     `json:"schema_version"`
	Target        string  `json:"target"`
	Route         Route   `json:"route"`
	LatencyMS     float64 `json:"latency_ms"`
}

func (result Result) Validate() error {
	if result.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported speed-test schema %d", result.SchemaVersion)
	}
	if result.StartedAt.IsZero() || result.EndedAt.IsZero() || result.EndedAt.Before(result.StartedAt) {
		return errors.New("speed-test timestamps are invalid")
	}
	if result.Target != TargetHost || result.Family != "ipv4" {
		return errors.New("speed-test target identity is invalid")
	}
	if result.Route != RouteDirect && result.Route != RouteNJUConnect {
		return errors.New("speed-test result route is invalid")
	}
	for name, value := range map[string]*float64{
		"ping": result.PingMS, "jitter": result.JitterMS,
		"download": result.DownloadMbps, "upload": result.UploadMbps,
	} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return fmt.Errorf("speed-test %s is invalid", name)
		}
	}
	switch result.Status {
	case StatusSuccess:
		if result.DownloadMbps == nil || result.UploadMbps == nil || result.Server == "" || result.HelperVersion == "" || result.Failure != nil {
			return errors.New("successful speed-test result is incomplete")
		}
	case StatusFailed, StatusCancelled:
		if result.Failure == nil || result.Failure.Code == "" || result.Failure.Stage == "" || result.Failure.Message == "" {
			return errors.New("unsuccessful speed-test result lacks a failure")
		}
	default:
		return errors.New("speed-test result status is invalid")
	}
	return nil
}

// ExitCode maps a result to the CLI convention: 0 for success, 1 for a failed
// measurement, and 130 for user cancellation. Exit code 2 stays reserved for
// usage errors.
func (result Result) ExitCode() int {
	switch result.Status {
	case StatusSuccess:
		return 0
	case StatusCancelled:
		return 130
	default:
		return 1
	}
}

type Event struct {
	SchemaVersion int      `json:"schema_version"`
	Type          string   `json:"type"`
	Phase         string   `json:"phase,omitempty"`
	Test          string   `json:"test,omitempty"`
	Mbps          *float64 `json:"mbps,omitempty"`
	Progress      *float64 `json:"progress,omitempty"`
	Message       string   `json:"message,omitempty"`
	Route         Route    `json:"route,omitempty"`
	Result        *Result  `json:"result,omitempty"`
}

type ProgressSink func(Event)

func report(sink ProgressSink, event Event) {
	if sink == nil {
		return
	}
	event.SchemaVersion = SchemaVersion
	sink(event)
}
