package gatewayauth

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
)

var ErrNoAuthenticatedSession = errors.New("gateway authentication is not complete")

// Session owns the short-lived authenticated gateway state. It deliberately has
// no persistence representation: cookies and the gateway session identifier live
// only until Close or process exit.
type Session struct {
	baseURL   *url.URL
	http      *http.Client
	sessionID []byte
}

type SessionState struct {
	CookieCount int
	HasID       bool
}

type Bootstrap struct {
	ConfigurationAvailable bool
	ResourcesAvailable     bool
}

// TakeSession transfers the authenticated HTTP client and its cookie jar out of
// Client. Authentication methods cannot be used after a successful transfer.
func (client *Client) TakeSession() (*Session, error) {
	if !client.authenticated || client.http == nil {
		return nil, ErrNoAuthenticatedSession
	}
	session := &Session{
		baseURL:   client.baseURL,
		http:      client.http,
		sessionID: append([]byte(nil), client.sessionID...),
	}
	clear(client.sessionID)
	client.sessionID = nil
	client.http = nil
	client.authenticated = false
	return session, nil
}

func (session *Session) State() SessionState {
	if session == nil || session.http == nil || session.baseURL == nil {
		return SessionState{}
	}
	return SessionState{
		CookieCount: len(session.http.Jar.Cookies(session.baseURL)),
		HasID:       len(session.sessionID) != 0,
	}
}

// ProbeBootstrap verifies the read-only gateway initialization immediately after
// authentication. It stops before any local agent, tunnel, route, or DNS change.
func (session *Session) ProbeBootstrap(ctx context.Context) (Bootstrap, error) {
	if session == nil || session.http == nil {
		return Bootstrap{}, ErrNoAuthenticatedSession
	}
	configuration, err := session.probeXML(ctx, "/por/conf.csp?apiversion=1", "Auth", "Conf")
	if err != nil {
		return Bootstrap{}, fmt.Errorf("read gateway configuration: %w", err)
	}
	resources, err := session.probeXML(ctx, "/por/rclist.csp?apiversion=1", "Auth", "Resource")
	if err != nil {
		return Bootstrap{}, fmt.Errorf("read gateway resources: %w", err)
	}
	return Bootstrap{ConfigurationAvailable: configuration, ResourcesAvailable: resources}, nil
}

func (session *Session) probeXML(ctx context.Context, path string, required ...string) (bool, error) {
	data, err := requestBytes(ctx, session.http, session.baseURL, http.MethodGet, path, nil)
	if err != nil {
		return false, err
	}
	wanted := make(map[string]bool, len(required))
	for _, name := range required {
		wanted[name] = false
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, decodeErr := decoder.Token()
		if decodeErr != nil {
			if errors.Is(decodeErr, io.EOF) {
				break
			}
			return false, fmt.Errorf("decode gateway XML: %w", decodeErr)
		}
		if start, ok := token.(xml.StartElement); ok {
			if _, exists := wanted[start.Name.Local]; exists {
				wanted[start.Name.Local] = true
			}
		}
	}
	for _, found := range wanted {
		if !found {
			return false, nil
		}
	}
	return true, nil
}

// Close removes references to all retained authentication material.
func (session *Session) Close() error {
	if session == nil {
		return nil
	}
	clear(session.sessionID)
	session.sessionID = nil
	if session.http != nil {
		if transport, ok := session.http.Transport.(interface{ CloseIdleConnections() }); ok {
			transport.CloseIdleConnections()
		}
		jar, err := cookiejar.New(nil)
		if err != nil {
			return err
		}
		session.http.Jar = jar
	}
	session.http = nil
	session.baseURL = nil
	return nil
}
