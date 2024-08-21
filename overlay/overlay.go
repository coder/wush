package overlay

import (
	"net/netip"

	"tailscale.com/tailcfg"
)

// Overlay specifies the mechanism by which senders and receivers exchange
// Tailscale nodes over a sidechannel.
type Overlay interface {
	// listenOverlay(ctx context.Context, kind string) error
	Recv() <-chan *tailcfg.Node
	Send() chan<- *tailcfg.Node
	IP() netip.Addr
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

	IP   netip.Addr
	Node tailcfg.Node
}
