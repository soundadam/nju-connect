package core

import (
	"testing"

	"github.com/soundadam/soundconnect/internal/backend/easyconnect/auth"
)

func TestBuildDataplanePlan(t *testing.T) {
	bootstrap := gatewayauth.Bootstrap{
		ConfigurationAvailable: true,
		ResourcesAvailable:     true,
		Resources:              gatewayauth.ResourceSummary{L3VPN: 6},
		Services: gatewayauth.ServiceRequirements{
			L3VPN:       true,
			InternalDNS: true,
		},
	}
	plan, err := BuildDataplanePlan(gatewayauth.SessionState{HasID: true}, bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != DataplaneL3VPN || plan.ResourceCount != 6 || !plan.InternalDNS || !plan.LocalAgentRequired || !plan.BoundaryReady {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestBuildDataplanePlanRequiresSessionIDAtAgentBoundary(t *testing.T) {
	bootstrap := gatewayauth.Bootstrap{
		ConfigurationAvailable: true,
		ResourcesAvailable:     true,
		Services:               gatewayauth.ServiceRequirements{L3VPN: true},
	}
	if _, err := BuildDataplanePlan(gatewayauth.SessionState{}, bootstrap); err == nil {
		t.Fatal("expected missing session identifier error")
	}
}

func TestBuildDataplanePlanWithoutLocalAgent(t *testing.T) {
	bootstrap := gatewayauth.Bootstrap{
		ConfigurationAvailable: true,
		ResourcesAvailable:     true,
		Resources:              gatewayauth.ResourceSummary{Web: 2},
	}
	plan, err := BuildDataplanePlan(gatewayauth.SessionState{}, bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != DataplaneNone || plan.LocalAgentRequired || plan.BoundaryReady {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestDataplaneMode(t *testing.T) {
	tests := []struct {
		requirements gatewayauth.ServiceRequirements
		want         DataplaneMode
	}{
		{want: DataplaneNone},
		{requirements: gatewayauth.ServiceRequirements{TCP: true}, want: DataplaneTCP},
		{requirements: gatewayauth.ServiceRequirements{L3VPN: true}, want: DataplaneL3VPN},
		{requirements: gatewayauth.ServiceRequirements{TCP: true, L3VPN: true}, want: DataplaneHybrid},
	}
	for _, test := range tests {
		if got := dataplaneMode(test.requirements); got != test.want {
			t.Fatalf("dataplaneMode(%+v) = %q, want %q", test.requirements, got, test.want)
		}
	}
}
