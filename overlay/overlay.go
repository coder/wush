package overlay

import (
	"net/netip"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"
	"tailscale.com/tailcfg"
)

type Logf func(format string, args ...any)

// Overlay specifies the mechanism by which senders and receivers exchange
// Tailscale nodes over a sidechannel.
type Overlay interface {
	// listenOverlay(ctx context.Context, kind string) error
	Recv() <-chan *tailcfg.Node
	SendTailscaleNodeUpdate(node *tailcfg.Node)
	IPs() []netip.Addr
}

type messageType int

const (
	messageTypePing messageType = 1 + iota
	messageTypePong
	messageTypeHello
	messageTypeHelloResponse
	messageTypeNodeUpdate

	messageTypeWebRTCOffer
	messageTypeWebRTCAnswer
	messageTypeWebRTCCandidate
)

type overlayMessage struct {
	Typ messageType

	HostInfo HostInfo
	Node     tailcfg.Node

	WebrtcDescription *webrtc.SessionDescription
	WebrtcCandidate   *webrtc.ICECandidateInit
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
