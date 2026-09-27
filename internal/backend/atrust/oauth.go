package atrustbackend

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/soundadam/nju-connect/internal/backend"
)

const oauthCallbackPath = "/passport/v1/auth/httpsOauth2"

// ParseOAuthCallbackCode validates the browser callback locally and returns
// only its short-lived authorization code. The callback must name the
// configured gateway identity (Endpoint.Host, never the pinned dial address);
// an explicit default :443 port is accepted because NJU includes it.
func ParseOAuthCallbackCode(raw string, endpoint backend.Endpoint) (string, error) {
	callback, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errors.New("invalid aTrust OAuth callback URL")
	}
	if callback.Scheme != "https" || callback.User != nil || callback.Fragment != "" {
		return "", errors.New("invalid aTrust OAuth callback URL")
	}
	if !strings.EqualFold(callback.Hostname(), endpoint.Host) {
		return "", errors.New("aTrust OAuth callback host does not match the gateway")
	}
	port := callback.Port()
	if port != "" && port != strconv.Itoa(endpoint.Port) {
		return "", errors.New("aTrust OAuth callback port does not match the gateway")
	}
	if callback.Path != oauthCallbackPath {
		return "", errors.New("invalid aTrust OAuth callback path")
	}
	code := callback.Query().Get("code")
	if code == "" {
		return "", errors.New("aTrust OAuth callback code is missing")
	}
	return code, nil
}
