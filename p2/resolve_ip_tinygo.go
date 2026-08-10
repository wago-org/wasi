//go:build tinygo

package p2

import (
	"context"
	"net"
)

func defaultResolveIP(_ context.Context, name string) ([]net.IP, error) {
	return net.LookupIP(name)
}
