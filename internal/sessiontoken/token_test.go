package sessiontoken

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

const fixtureSessionID = "0123456789abcdef"
const fixtureLocalControlToken = "172db4bc8d7722e5b084215a4cafdff0"

func TestLocalControlTokenMatchesDeterministicFixtureAndIsCleared(t *testing.T) {
	sessionID := []byte(fixtureSessionID)
	var borrowed LocalControlToken
	if err := WithLocalControlToken(sessionID, func(token LocalControlToken) error {
		borrowed = token
		if got := string(token); got != fixtureLocalControlToken {
			t.Fatalf("local-control token differs from fixture")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(borrowed, make([]byte, LocalControlTokenSize)) {
		t.Fatal("borrowed local-control token was not cleared")
	}
	if string(sessionID) != fixtureSessionID {
		t.Fatal("owned gateway session identifier was modified")
	}
}

func TestNativeGatewayTokenUsesOnlyObservedSSLContextSlice(t *testing.T) {
	decoded := make([]byte, sslContextDecodedSize)
	for index := range decoded {
		decoded[index] = byte(index)
	}
	encoded := make([]byte, hex.EncodedLen(len(decoded)))
	hex.Encode(encoded, decoded)
	token, err := DecodeNativeGatewayToken(encoded)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(token)
	want := append(make([]byte, NativeGatewaySessionFieldOffset), decoded[32:48]...)
	if !bytes.Equal(token, want) {
		t.Fatalf("native gateway token layout differs")
	}
	if bytes.Equal(token[:LocalControlTokenSize], []byte(fixtureLocalControlToken)) {
		t.Fatal("native gateway token reused the ECAgent local-control token")
	}
}

func TestTokenErrorsAreFixedAndDoNotExposeMaterial(t *testing.T) {
	sentinel := errors.New("consumer stopped")
	err := WithLocalControlToken([]byte(fixtureSessionID), func(LocalControlToken) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("consumer error = %v", err)
	}

	secret := "short-sensitive-session"
	for _, err := range []error{
		WithLocalControlToken([]byte(secret), func(LocalControlToken) error { return nil }),
		WithLocalControlToken([]byte(fixtureSessionID), nil),
		decodeNativeError([]byte(secret)),
		decodeNativeError(bytes.Repeat([]byte{'z'}, GatewaySSLContextHexSize)),
	} {
		if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), fixtureSessionID) {
			t.Fatalf("unsafe token error = %v", err)
		}
	}
}

func decodeNativeError(value []byte) error {
	token, err := DecodeNativeGatewayToken(value)
	clear(token)
	return err
}
