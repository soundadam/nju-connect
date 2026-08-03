package gatewayauth

import (
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
)

var ErrNoAuthenticatedSession = errors.New("gateway authentication is not complete")
var ErrNoSessionID = errors.New("gateway session identifier is unavailable")

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

// WithID lends a copy of the gateway session identifier to one synchronous
// operation and clears that copy immediately afterward.
func (session *Session) WithID(use func([]byte) error) error {
	if session == nil || session.http == nil {
		return ErrNoAuthenticatedSession
	}
	if len(session.sessionID) == 0 {
		return ErrNoSessionID
	}
	id := append([]byte(nil), session.sessionID...)
	defer clear(id)
	return use(id)
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
