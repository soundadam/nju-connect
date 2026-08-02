package gatewayauth

import "testing"

func TestTakeSessionRequiresAcceptedAuthentication(t *testing.T) {
	client := &Client{}
	if _, err := client.TakeSession(); err != ErrNoAuthenticatedSession {
		t.Fatalf("TakeSession error = %v", err)
	}
}

func TestClosedSessionHasNoState(t *testing.T) {
	session := &Session{sessionID: []byte("secret")}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if state := session.State(); state != (SessionState{}) {
		t.Fatalf("closed session state = %+v", state)
	}
}
