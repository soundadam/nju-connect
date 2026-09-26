package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/soundadam/soundconnect/internal/backend/easyconnect/auth"
	"github.com/soundadam/soundconnect/internal/core"
	"github.com/soundadam/soundconnect/internal/credential"
)

// DryRunReport is how far a dry run got. DryRun fills it in step by step,
// so it is meaningful alongside an error too.
type DryRunReport struct {
	// Accepted is true once the gateway accepted the sign-in.
	Accepted bool
	// SessionRetained is true once the authenticated session was kept.
	SessionRetained bool
	// Bootstrap is the gateway's advertised configuration, once it was
	// found complete.
	Bootstrap *gatewayauth.Bootstrap
	// Plan is the dataplane the connection would start.
	Plan *core.DataplanePlan
}

// DryRun signs in to the EasyConnect gateway and checks what the gateway
// hands over, without starting the dataplane.
func DryRun(ctx context.Context, deps Deps) (DryRunReport, error) {
	var report DryRunReport
	paths, err := deps.Paths()
	if err != nil {
		return report, fmt.Errorf("resolve local state: %w", err)
	}
	configured, err := LoadProfile(ctx, deps, true)
	if err != nil {
		return report, err
	}
	password, err := readSavedPassword(deps, paths, configured)
	if err != nil {
		return report, err
	}
	defer credential.Clear(password)
	client, err := gatewayauth.New(gatewayauth.Options{
		Server:      configured.Server,
		TLSInsecure: configured.TLSInsecure, UpstreamProxy: configured.UpstreamProxy,
		Timeout: gatewayAuthTimeout,
	})
	if err != nil {
		return report, fmt.Errorf("prepare gateway authentication: %w", err)
	}
	defer client.Close()
	result, err := client.AuthenticatePassword(ctx, configured.Username, password)
	if err != nil {
		return report, fmt.Errorf("authenticate password: %w", err)
	}
	if result.NeedsSMS() {
		if err = client.PrepareSMS(ctx); err != nil {
			return report, fmt.Errorf("prepare verification code authentication: %w", err)
		}
		code, err := deps.Interaction.VerificationCode(ctx, "")
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return report, err
			}
			return report, fmt.Errorf("read verification code: %w", err)
		}
		defer credential.Clear(code)
		result, err = client.AuthenticateSMS(ctx, code)
		if err != nil {
			return report, fmt.Errorf("authenticate verification code: %w", err)
		}
	}
	if !result.Accepted() {
		if result.NextService != "" {
			return report, fmt.Errorf("authentication requires unsupported next step %q (gateway code %d)", result.NextService, result.Code)
		}
		return report, fmt.Errorf("authentication rejected by gateway code %d", result.Code)
	}
	report.Accepted = true
	session, err := client.TakeSession()
	if err != nil {
		return report, fmt.Errorf("retain authenticated session: %w", err)
	}
	defer session.Close()
	report.SessionRetained = true
	bootstrap, err := session.ProbeBootstrap(ctx)
	if err != nil {
		return report, fmt.Errorf("probe gateway bootstrap: %w", err)
	}
	if !bootstrap.ConfigurationAvailable || !bootstrap.ResourcesAvailable {
		return report, fmt.Errorf("probe gateway bootstrap: configuration_available=%t resources_available=%t",
			bootstrap.ConfigurationAvailable, bootstrap.ResourcesAvailable)
	}
	report.Bootstrap = &bootstrap
	plan, err := core.BuildDataplanePlan(session.State(), bootstrap)
	if err != nil {
		return report, fmt.Errorf("model dataplane boundary: %w", err)
	}
	report.Plan = &plan
	return report, nil
}
