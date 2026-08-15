package p2

import (
	"context"

	component "github.com/wago-org/component-model"
)

const (
	ifaceNetwork         = "wasi:sockets/network@0.2.0"
	ifaceInstanceNetwork = "wasi:sockets/instance-network@0.2.0"
	ifaceTCPCreate       = "wasi:sockets/tcp-create-socket@0.2.0"
	ifaceUDPCreate       = "wasi:sockets/udp-create-socket@0.2.0"
	ifaceIPNameLookup    = "wasi:sockets/ip-name-lookup@0.2.0"
)

const socketAccessDenied uint32 = 1

// socketOptions deliberately contains no net.Conn, net.Dialer, listener, or
// resolver. It implements the socket capability boundary while networking is
// disabled: attempts become ordinary WASI access-denied results, not missing
// import traps and not ambient host network access.
func socketOptions() []component.Option {
	instanceNetwork := func(context.Context, []component.Value) ([]component.Value, error) {
		return []component.Value{uint32(1)}, nil
	}
	denied := func(context.Context, []component.Value) ([]component.Value, error) {
		return []component.Value{component.ResultValue{IsErr: true, Payload: socketAccessDenied}}, nil
	}
	return []component.Option{
		component.WithResourceTag(ifaceNetwork, "network", networkResource),
		component.WithResourceTag("wasi:sockets/tcp@0.2.0", "tcp-socket", tcpSocketResource),
		component.WithResourceTag("wasi:sockets/udp@0.2.0", "udp-socket", udpSocketResource),
		component.WithResourceTag(ifaceIPNameLookup, "resolve-address-stream", resolveStreamResource),
		component.WithImport(ifaceInstanceNetwork, "instance-network", instanceNetwork, nil, []component.TypeDesc{component.OwnDesc{ResourceType: networkResource}}),
		custom(ifaceTCPCreate, "create-tcp-socket", denied, createTCPSocketDesc),
		custom(ifaceUDPCreate, "create-udp-socket", denied, createUDPSocketDesc),
		custom(ifaceIPNameLookup, "resolve-addresses", denied, resolveAddressesDesc),
	}
}

func socketErrorCode(t *component.TypeTable) component.TypeRef {
	return t.Enum(
		"unknown", "access-denied", "not-supported", "invalid-argument", "out-of-memory",
		"timeout", "concurrency-conflict", "not-in-progress", "would-block", "invalid-state",
		"new-socket-limit", "address-not-bindable", "address-in-use", "remote-unreachable",
		"connection-refused", "connection-reset", "connection-aborted", "datagram-too-large",
		"name-unresolvable", "temporary-resolver-failure", "permanent-resolver-failure",
	)
}

func createTCPSocketDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Enum("ipv4", "ipv6")}, t.Result(t.Own(tcpSocketResource), socketErrorCode(t)))
}

func createUDPSocketDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Enum("ipv4", "ipv6")}, t.Result(t.Own(udpSocketResource), socketErrorCode(t)))
}

func resolveAddressesDesc(t *component.TypeTable) component.FuncDesc {
	return t.Func([]component.TypeRef{t.Borrow(networkResource), component.Prim("string")}, t.Result(t.Own(resolveStreamResource), socketErrorCode(t)))
}
