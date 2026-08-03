// Package sessiontoken owns the two distinct token layouts associated with an
// authenticated EasyConnect gateway session.
package sessiontoken

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
)

const (
	GatewaySessionIDSize            = 16
	LocalControlTokenSize           = 32
	NativeGatewayTokenSize          = 48
	NativeGatewaySessionFieldOffset = 32
	NativeGatewaySessionFieldSize   = 16
	GatewaySSLContextHexSize        = 128

	localControlTokenSalt = "__md5_salt_for_ecagent_session__"
	sslContextDecodedSize = 64
)

var ErrInvalidGatewaySessionID = errors.New("gateway session identifier has invalid length")
var ErrInvalidGatewaySSLContext = errors.New("gateway SSL context has invalid encoding")
var ErrTokenConsumerRequired = errors.New("session token consumer is required")

// LocalControlToken authenticates only the research-only ECAgent loopback
// control endpoint. It is not a native gateway token.
type LocalControlToken []byte

// NativeGatewayToken authenticates the native command and data-stream
// protocols. The observed wire layout is 32 zero bytes followed by the
// 16-byte session field extracted from the authenticated gateway SSL context.
type NativeGatewayToken []byte

// WithLocalControlToken lends a short-lived ECAgent loopback-control token and
// clears it as soon as the synchronous consumer returns.
func WithLocalControlToken(sessionID []byte, use func(LocalControlToken) error) error {
	if len(sessionID) != GatewaySessionIDSize {
		return ErrInvalidGatewaySessionID
	}
	if use == nil {
		return ErrTokenConsumerRequired
	}
	token := make(LocalControlToken, LocalControlTokenSize)
	material := make([]byte, 0, len(sessionID)+len(localControlTokenSalt))
	material = append(material, sessionID...)
	material = append(material, localControlTokenSalt...)
	digest := md5.Sum(material) // Required by the observed ECAgent loopback protocol.
	clear(material)
	hex.Encode(token, digest[:])
	clear(digest[:])
	defer clear(token)
	return use(token)
}

// DecodeNativeGatewayToken decodes the authenticated gateway's 128-byte hex
// sslctx field and returns a newly owned native protocol token. The caller must
// clear the result after use or when transferring ownership.
func DecodeNativeGatewayToken(sslContextHex []byte) (NativeGatewayToken, error) {
	if len(sslContextHex) != GatewaySSLContextHexSize {
		return nil, ErrInvalidGatewaySSLContext
	}
	decoded := make([]byte, sslContextDecodedSize)
	defer clear(decoded)
	if _, err := hex.Decode(decoded, sslContextHex); err != nil {
		return nil, ErrInvalidGatewaySSLContext
	}
	token := make(NativeGatewayToken, NativeGatewayTokenSize)
	fieldEnd := NativeGatewaySessionFieldOffset + NativeGatewaySessionFieldSize
	copy(token[NativeGatewaySessionFieldOffset:fieldEnd], decoded[NativeGatewaySessionFieldOffset:fieldEnd])
	return token, nil
}
