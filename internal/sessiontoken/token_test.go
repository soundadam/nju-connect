package sessiontoken

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

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
}

func TestTokenErrorsAreFixedAndDoNotExposeMaterial(t *testing.T) {
	secret := "short-sensitive-session"
	for _, err := range []error{
		decodeNativeError([]byte(secret)),
		decodeNativeError(bytes.Repeat([]byte{'z'}, GatewaySSLContextHexSize)),
	} {
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("unsafe token error = %v", err)
		}
	}
}

func decodeNativeError(value []byte) error {
	token, err := DecodeNativeGatewayToken(value)
	clear(token)
	return err
}
