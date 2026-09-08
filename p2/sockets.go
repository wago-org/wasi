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
	ifaceTCP             = "wasi:sockets/tcp@0.2.0"
	ifaceUDP             = "wasi:sockets/udp@0.2.0"
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
	unreachable := func(context.Context, []component.Value) ([]component.Value, error) { return nil, context.Canceled }
	falseValue := func(context.Context, []component.Value) ([]component.Value, error) {
		return []component.Value{false}, nil
	}
	zeroValue := func(context.Context, []component.Value) ([]component.Value, error) {
		return []component.Value{uint32(0)}, nil
	}
	opts := []component.Option{
		component.WithResourceTag(ifaceNetwork, "network", networkResource),
		component.WithResourceTag("wasi:sockets/tcp@0.2.0", "tcp-socket", tcpSocketResource),
		component.WithResourceTag("wasi:sockets/udp@0.2.0", "udp-socket", udpSocketResource),
		component.WithResourceTag(ifaceIPNameLookup, "resolve-address-stream", resolveStreamResource),
		component.WithResourceTag(ifaceUDP, "incoming-datagram-stream", incomingDatagramResource),
		component.WithResourceTag(ifaceUDP, "outgoing-datagram-stream", outgoingDatagramResource),
		custom(ifaceInstanceNetwork, "instance-network", instanceNetwork, func(t *component.TypeTable) component.FuncDesc { return t.Func(nil, t.Own(networkResource)) }),
		custom(ifaceTCPCreate, "create-tcp-socket", denied, createTCPSocketDesc),
		custom(ifaceUDPCreate, "create-udp-socket", denied, createUDPSocketDesc),
		custom(ifaceIPNameLookup, "resolve-addresses", denied, resolveAddressesDesc),
	}
	for _, spec := range []struct {
		name string
		desc func(*component.TypeTable) component.FuncDesc
	}{
		{"start-bind", tcpStartBindDesc}, {"finish-bind", sockEmptyMethod(tcpSocketResource)}, {"start-connect", tcpStartConnectDesc}, {"finish-connect", tcpFinishConnectDesc}, {"start-listen", sockEmptyMethod(tcpSocketResource)}, {"finish-listen", sockEmptyMethod(tcpSocketResource)}, {"accept", tcpAcceptDesc}, {"local-address", sockAddressMethod(tcpSocketResource)}, {"remote-address", sockAddressMethod(tcpSocketResource)},
		{"set-listen-backlog-size", sockSetter(tcpSocketResource, "u64")}, {"keep-alive-enabled", sockScalarResult(tcpSocketResource, "bool")}, {"set-keep-alive-enabled", sockSetter(tcpSocketResource, "bool")}, {"keep-alive-idle-time", sockScalarResult(tcpSocketResource, "u64")}, {"set-keep-alive-idle-time", sockSetter(tcpSocketResource, "u64")}, {"keep-alive-interval", sockScalarResult(tcpSocketResource, "u64")}, {"set-keep-alive-interval", sockSetter(tcpSocketResource, "u64")}, {"keep-alive-count", sockScalarResult(tcpSocketResource, "u32")}, {"set-keep-alive-count", sockSetter(tcpSocketResource, "u32")}, {"hop-limit", sockScalarResult(tcpSocketResource, "u8")}, {"set-hop-limit", sockSetter(tcpSocketResource, "u8")}, {"receive-buffer-size", sockScalarResult(tcpSocketResource, "u64")}, {"set-receive-buffer-size", sockSetter(tcpSocketResource, "u64")}, {"send-buffer-size", sockScalarResult(tcpSocketResource, "u64")}, {"set-send-buffer-size", sockSetter(tcpSocketResource, "u64")}, {"shutdown", tcpShutdownDesc},
	} {
		opts = append(opts, custom(ifaceTCP, "[method]tcp-socket."+spec.name, denied, spec.desc))
	}
	opts = append(opts, custom(ifaceTCP, "[method]tcp-socket.is-listening", falseValue, sockBoolMethod(tcpSocketResource)), custom(ifaceTCP, "[method]tcp-socket.address-family", zeroValue, sockFamilyMethod(tcpSocketResource)), custom(ifaceTCP, "[method]tcp-socket.subscribe", unreachable, sockSubscribe(tcpSocketResource)))
	for _, spec := range []struct {
		name string
		desc func(*component.TypeTable) component.FuncDesc
	}{
		{"start-bind", udpStartBindDesc}, {"finish-bind", sockEmptyMethod(udpSocketResource)}, {"stream", udpStreamDesc}, {"local-address", sockAddressMethod(udpSocketResource)}, {"remote-address", sockAddressMethod(udpSocketResource)}, {"unicast-hop-limit", sockScalarResult(udpSocketResource, "u8")}, {"set-unicast-hop-limit", sockSetter(udpSocketResource, "u8")}, {"receive-buffer-size", sockScalarResult(udpSocketResource, "u64")}, {"set-receive-buffer-size", sockSetter(udpSocketResource, "u64")}, {"send-buffer-size", sockScalarResult(udpSocketResource, "u64")}, {"set-send-buffer-size", sockSetter(udpSocketResource, "u64")},
	} {
		opts = append(opts, custom(ifaceUDP, "[method]udp-socket."+spec.name, denied, spec.desc))
	}
	opts = append(opts, custom(ifaceUDP, "[method]udp-socket.address-family", zeroValue, sockFamilyMethod(udpSocketResource)), custom(ifaceUDP, "[method]udp-socket.subscribe", unreachable, sockSubscribe(udpSocketResource)))
	opts = append(opts, custom(ifaceUDP, "[method]incoming-datagram-stream.receive", denied, udpReceiveDesc), custom(ifaceUDP, "[method]incoming-datagram-stream.subscribe", unreachable, sockSubscribe(incomingDatagramResource)), custom(ifaceUDP, "[method]outgoing-datagram-stream.check-send", denied, sockScalarResult(outgoingDatagramResource, "u64")), custom(ifaceUDP, "[method]outgoing-datagram-stream.send", denied, udpSendDesc), custom(ifaceUDP, "[method]outgoing-datagram-stream.subscribe", unreachable, sockSubscribe(outgoingDatagramResource)))
	opts = append(opts, custom(ifaceIPNameLookup, "[method]resolve-address-stream.resolve-next-address", denied, resolveNextDesc), custom(ifaceIPNameLookup, "[method]resolve-address-stream.subscribe", unreachable, sockSubscribe(resolveStreamResource)))
	return opts
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

func ipFamily(t *component.TypeTable) component.TypeRef { return t.Enum("ipv4", "ipv6") }
func ipAddress(t *component.TypeTable) component.TypeRef {
	return t.Variant(component.VariantCaseSpec{Name: "ipv4", Type: t.Tuple(component.Prim("u8"), component.Prim("u8"), component.Prim("u8"), component.Prim("u8"))}, component.VariantCaseSpec{Name: "ipv6", Type: t.Tuple(component.Prim("u16"), component.Prim("u16"), component.Prim("u16"), component.Prim("u16"), component.Prim("u16"), component.Prim("u16"), component.Prim("u16"), component.Prim("u16"))})
}
func ipSocketAddress(t *component.TypeTable) component.TypeRef {
	v4 := t.Record("port", component.Prim("u16"), "address", t.Tuple(component.Prim("u8"), component.Prim("u8"), component.Prim("u8"), component.Prim("u8")))
	v6 := t.Record("port", component.Prim("u16"), "flow-info", component.Prim("u32"), "address", t.Tuple(component.Prim("u16"), component.Prim("u16"), component.Prim("u16"), component.Prim("u16"), component.Prim("u16"), component.Prim("u16"), component.Prim("u16"), component.Prim("u16")), "scope-id", component.Prim("u32"))
	return t.Variant(component.VariantCaseSpec{Name: "ipv4", Type: v4}, component.VariantCaseSpec{Name: "ipv6", Type: v6})
}
func sockResult(t *component.TypeTable, ok component.TypeRef) component.TypeRef {
	return t.Result(ok, socketErrorCode(t))
}
func sockMethod(resource uint32, params []component.TypeRef, result component.TypeRef, t *component.TypeTable) component.FuncDesc {
	return t.Func(append([]component.TypeRef{t.Borrow(resource)}, params...), result)
}
func sockEmptyMethod(resource uint32) func(*component.TypeTable) component.FuncDesc {
	return func(t *component.TypeTable) component.FuncDesc {
		return sockMethod(resource, nil, sockResult(t, component.TypeRef{}), t)
	}
}
func sockScalarResult(resource uint32, prim string) func(*component.TypeTable) component.FuncDesc {
	return func(t *component.TypeTable) component.FuncDesc {
		return sockMethod(resource, nil, sockResult(t, component.Prim(prim)), t)
	}
}
func sockSetter(resource uint32, prim string) func(*component.TypeTable) component.FuncDesc {
	return func(t *component.TypeTable) component.FuncDesc {
		return sockMethod(resource, []component.TypeRef{component.Prim(prim)}, sockResult(t, component.TypeRef{}), t)
	}
}
func sockAddressMethod(resource uint32) func(*component.TypeTable) component.FuncDesc {
	return func(t *component.TypeTable) component.FuncDesc {
		return sockMethod(resource, nil, sockResult(t, ipSocketAddress(t)), t)
	}
}
func sockBoolMethod(resource uint32) func(*component.TypeTable) component.FuncDesc {
	return func(t *component.TypeTable) component.FuncDesc {
		return sockMethod(resource, nil, component.Prim("bool"), t)
	}
}
func sockFamilyMethod(resource uint32) func(*component.TypeTable) component.FuncDesc {
	return func(t *component.TypeTable) component.FuncDesc { return sockMethod(resource, nil, ipFamily(t), t) }
}
func sockSubscribe(resource uint32) func(*component.TypeTable) component.FuncDesc {
	return func(t *component.TypeTable) component.FuncDesc {
		return sockMethod(resource, nil, t.Own(pollableResource), t)
	}
}
func tcpStartBindDesc(t *component.TypeTable) component.FuncDesc {
	return sockMethod(tcpSocketResource, []component.TypeRef{t.Borrow(networkResource), ipSocketAddress(t)}, sockResult(t, component.TypeRef{}), t)
}
func tcpStartConnectDesc(t *component.TypeTable) component.FuncDesc { return tcpStartBindDesc(t) }
func tcpFinishConnectDesc(t *component.TypeTable) component.FuncDesc {
	return sockMethod(tcpSocketResource, nil, sockResult(t, t.Tuple(t.Own(inputStreamResource), t.Own(outputStreamResource))), t)
}
func tcpAcceptDesc(t *component.TypeTable) component.FuncDesc {
	return sockMethod(tcpSocketResource, nil, sockResult(t, t.Tuple(t.Own(tcpSocketResource), t.Own(inputStreamResource), t.Own(outputStreamResource))), t)
}
func tcpShutdownDesc(t *component.TypeTable) component.FuncDesc {
	return sockMethod(tcpSocketResource, []component.TypeRef{t.Enum("receive", "send", "both")}, sockResult(t, component.TypeRef{}), t)
}
func udpStartBindDesc(t *component.TypeTable) component.FuncDesc {
	return sockMethod(udpSocketResource, []component.TypeRef{t.Borrow(networkResource), ipSocketAddress(t)}, sockResult(t, component.TypeRef{}), t)
}
func udpStreamDesc(t *component.TypeTable) component.FuncDesc {
	return sockMethod(udpSocketResource, []component.TypeRef{t.Option(ipSocketAddress(t))}, sockResult(t, t.Tuple(t.Own(incomingDatagramResource), t.Own(outgoingDatagramResource))), t)
}
func udpReceiveDesc(t *component.TypeTable) component.FuncDesc {
	entry := t.Record("data", t.List(component.Prim("u8")), "remote-address", ipSocketAddress(t))
	return sockMethod(incomingDatagramResource, []component.TypeRef{component.Prim("u64")}, sockResult(t, t.List(entry)), t)
}
func udpSendDesc(t *component.TypeTable) component.FuncDesc {
	entry := t.Record("data", t.List(component.Prim("u8")), "remote-address", t.Option(ipSocketAddress(t)))
	return sockMethod(outgoingDatagramResource, []component.TypeRef{t.List(entry)}, sockResult(t, component.Prim("u64")), t)
}
func resolveNextDesc(t *component.TypeTable) component.FuncDesc {
	return sockMethod(resolveStreamResource, nil, sockResult(t, t.Option(ipAddress(t))), t)
}
