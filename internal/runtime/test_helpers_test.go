package runtime

import (
	"context"
	"net"
)

type testTCPDialFunc func(context.Context, string, string) (net.Conn, error)

func (dial testTCPDialFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return dial(ctx, network, address)
}
