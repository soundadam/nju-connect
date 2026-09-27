// Package sessiontoken owns the native gateway token derived from an
// authenticated EasyConnect gateway session.
package sessiontoken

import (
	"encoding/hex"
	"errors"
)

const (
	NativeGatewayTokenSize          = 48
	NativeGatewaySessionFieldOffset = 32
	NativeGatewaySessionFieldSize   = 16
	GatewaySSLContextHexSize        = 128

	sslContextDecodedSize = 64
)

var ErrInvalidGatewaySSLContext = errors.New("gateway SSL context has invalid encoding")
var ErrTokenConsumerRequired = errors.New("session token consumer is required")

// NativeGatewayToken authenticates the native command and data-stream
// protocols. The observed wire layout is 32 zero bytes followed by the
// 16-byte session field extracted from the authenticated gateway SSL context.
type NativeGatewayToken []byte

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
