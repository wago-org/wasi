//go:build !tinygo

package p2

import (
	"context"
	"net"
)

func defaultResolveIP(ctx context.Context, name string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", name)
}
