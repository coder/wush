package overlay

import (
	"net/netip"

	"github.com/google/uuid"
	"tailscale.com/tailcfg"
)

// Overlay specifies the mechanism by which senders and receivers exchange
// Tailscale nodes over a sidechannel.
type Overlay interface {
	// listenOverlay(ctx context.Context, kind string) error
	Recv() <-chan *tailcfg.Node
	Send() chan<- *tailcfg.Node
	IPs() []netip.Addr
}

type messageType int

const (
	messageTypePing messageType = 1 + iota
	messageTypePong
	messageTypeHello
	messageTypeHelloResponse
	messageTypeNodeUpdate
)

type overlayMessage struct {
	Typ messageType

	HostInfo HostInfo
	Node     tailcfg.Node
}

type HostInfo struct {
	Username string
	Hostname string
}

var TailscaleServicePrefix6 = [6]byte{0xfd, 0x7a, 0x11, 0x5c, 0xa1, 0xe0}

func randv6() netip.Addr {
	uid := uuid.New()
	copy(uid[:], TailscaleServicePrefix6[:])
	return netip.AddrFrom16(uid)
}
