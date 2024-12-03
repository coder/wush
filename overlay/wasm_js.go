//go:build js && wasm

package overlay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"syscall/js"
	"time"

	"github.com/coder/wush/cliui"
	"github.com/pion/webrtc/v4"
	"github.com/puzpuzpuz/xsync/v3"
	"tailscale.com/derp"
	"tailscale.com/derp/derphttp"
	"tailscale.com/net/netcheck"
	"tailscale.com/net/netmon"
	"tailscale.com/net/portmapper"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/logger"
)

func NewWasmOverlay(hlog Logf, dm *tailcfg.DERPMap,
	onNewPeer js.Value,
	onWebrtcOffer js.Value,
	onWebrtcAnswer js.Value,
	onWebrtcCandidate js.Value,
) *Wasm {
	return &Wasm{
		HumanLogf: hlog,
		DerpMap:   dm,
		SelfPriv:  key.NewNode(),
		PeerPriv:  key.NewNode(),
		SelfIP:    randv6(),

		onNewPeer:         onNewPeer,
		onWebrtcOffer:     onWebrtcOffer,
		onWebrtcAnswer:    onWebrtcAnswer,
		onWebrtcCandidate: onWebrtcCandidate,

		in:  make(chan *tailcfg.Node, 8),
		out: make(chan *overlayMessage, 8),
	}
}

type Wasm struct {
	Logger    *slog.Logger
	HumanLogf Logf
	DerpMap   *tailcfg.DERPMap
	// SelfPriv is the private key that peers will encrypt overlay messages to.
	// The public key of this is sent in the auth key.
	SelfPriv key.NodePrivate
	// PeerPriv is the main auth mechanism used to secure the overlay. Peers are
	// sent this private key to encrypt node communication. Leaking this private
	// key would allow anyone to connect.
	PeerPriv key.NodePrivate
	SelfIP   netip.Addr

	// username is a randomly generated human-readable string displayed on
	// wush.dev to identify clients.
	username string

	// DerpRegionID is the DERP region that can be used for proxied overlay
	// communication.
	DerpRegionID uint16
	DerpLatency  time.Duration

	// peers is a map of channels that notify peers of node updates.
	activePeer atomic.Pointer[chan *overlayMessage]
	onNewPeer  js.Value

	onWebrtcOffer     js.Value
	onWebrtcAnswer    js.Value
	onWebrtcCandidate js.Value

	lastNode atomic.Pointer[tailcfg.Node]
	// in funnels node updates from other peers to us
	in chan *tailcfg.Node
	// out fans out our node updates to peers connected to us
	out chan *overlayMessage
}

func (r *Wasm) IPs() []netip.Addr {
	return []netip.Addr{r.SelfIP}
}

func (r *Wasm) PickDERPHome(ctx context.Context) error {
	nm := netmon.NewStatic()
	nc := netcheck.Client{
		NetMon:     nm,
		PortMapper: portmapper.NewClient(func(format string, args ...any) {}, nm, nil, nil, nil),
		Logf:       func(format string, args ...any) {},
	}

	report, err := nc.GetReport(ctx, r.DerpMap, nil)
	if err != nil {
		return err
	}

	if report.PreferredDERP == 0 {
		r.HumanLogf("Failed to determine overlay DERP region, defaulting to %s.", cliui.Code("NYC"))
		r.DerpRegionID = 1
		r.DerpLatency = report.RegionLatency[1]
	} else {
		r.HumanLogf("Picked DERP region %s as overlay home", cliui.Code(r.DerpMap.Regions[report.PreferredDERP].RegionName))
		r.DerpRegionID = uint16(report.PreferredDERP)
		r.DerpLatency = report.RegionLatency[report.PreferredDERP]
	}

	return nil
}

func (r *Wasm) ClientAuth() *ClientAuth {
	return &ClientAuth{
		Web:                  true,
		OverlayPrivateKey:    r.PeerPriv,
		ReceiverPublicKey:    r.SelfPriv.Public(),
		ReceiverDERPRegionID: r.DerpRegionID,
	}
}

func (r *Wasm) Recv() <-chan *tailcfg.Node {
	return r.in
}

func (r *Wasm) SendTailscaleNodeUpdate(node *tailcfg.Node) {
	r.out <- &overlayMessage{
		Typ:  messageTypeNodeUpdate,
		Node: *node.Clone(),
	}
}

func (r *Wasm) SendWebrtcCandidate(peer string, cand webrtc.ICECandidateInit) {

	fmt.Println("go: sending webrtc candidate")
	r.out <- &overlayMessage{
		Typ:             messageTypeWebRTCCandidate,
		WebrtcCandidate: &cand,
	}
}

type Peer struct {
	ID   string
	Name string
	IP   netip.Addr
	Type string
}

func (r *Wasm) Connect(ctx context.Context, ca ClientAuth, offer webrtc.SessionDescription) (Peer, error) {
	derpPriv := key.NewNode()
	c := derphttp.NewRegionClient(derpPriv, logger.Logf(r.HumanLogf), netmon.NewStatic(), func() *tailcfg.DERPRegion {
		return r.DerpMap.Regions[int(ca.ReceiverDERPRegionID)]
	})

	err := c.Connect(ctx)
	if err != nil {
		return Peer{}, err
	}

	sealed := r.newHelloPacket(ca, offer)
	err = c.Send(ca.ReceiverPublicKey, sealed)
	if err != nil {
		return Peer{}, fmt.Errorf("send overlay hello over derp: %w", err)
	}

	updates := make(chan *overlayMessage, 8)

	old := r.activePeer.Swap(&updates)
	if old != nil {
		close(*old)
	}

	go func() {
		defer r.activePeer.CompareAndSwap(&updates, nil)
		defer c.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-updates:
				if !ok {
					c.Close()
					return
				}

				raw, err := json.Marshal(msg)
				if err != nil {
					panic("marshal overlay msg: " + err.Error())
				}

				sealed := ca.OverlayPrivateKey.SealTo(ca.ReceiverPublicKey, raw)
				err = c.Send(ca.ReceiverPublicKey, sealed)
				if err != nil {
					fmt.Printf("send response over derp: %s\n", err)
					return
				}
			}
		}
	}()

	waitHello := make(chan struct{})
	closeOnce := sync.Once{}
	helloResp := overlayMessage{}
	helloSrc := key.NodePublic{}

	go func() {
		for {
			msg, err := c.Recv()
			if err != nil {
				fmt.Println("Recv derp:", err)
				return
			}

			switch msg := msg.(type) {
			case derp.ReceivedPacket:
				if ca.ReceiverPublicKey != msg.Source {
					fmt.Printf("message from unknown peer %s\n", msg.Source.String())
					continue
				}

				res, _, ovmsg, err := r.handleNextMessage(msg.Source, ca.OverlayPrivateKey, ca.ReceiverPublicKey, msg.Data)
				if err != nil {
					fmt.Println("Failed to handle overlay message:", err)
					continue
				}

				if res != nil {
					err = c.Send(msg.Source, res)
					if err != nil {
						fmt.Println(cliui.Timestamp(time.Now()), "Failed to send overlay response over derp:", err.Error())
						return
					}
				}

				if ovmsg.Typ == messageTypeHelloResponse {
					helloResp = ovmsg
					helloSrc = msg.Source
					closeOnce.Do(func() {
						close(waitHello)
					})
				}
			}
		}
	}()

	select {
	case <-time.After(10 * time.Second):
		c.Close()
		return Peer{}, errors.New("timed out waiting for peer to respond")
	case <-waitHello:
		updates <- &overlayMessage{
			Typ:  messageTypeNodeUpdate,
			Node: *r.lastNode.Load(),
		}
		if len(helloResp.Node.Addresses) == 0 {
			return Peer{}, fmt.Errorf("peer has no addresses")
		}
		ip := helloResp.Node.Addresses[0].Addr()
		typ := "cli"
		if ca.Web {
			typ = "web"
		}
		return Peer{
			ID:   helloSrc.String(),
			IP:   ip,
			Name: helloResp.HostInfo.Username,
			Type: typ,
		}, nil
	}
}

func (r *Wasm) ListenOverlayDERP(ctx context.Context) error {
	c := derphttp.NewRegionClient(r.SelfPriv, func(format string, args ...any) {}, netmon.NewStatic(), func() *tailcfg.DERPRegion {
		return r.DerpMap.Regions[int(r.DerpRegionID)]
	})
	defer c.Close()

	err := c.Connect(ctx)
	if err != nil {
		return err
	}

	// node pub -> derp pub
	peers := xsync.NewMapOf[key.NodePublic, key.NodePublic]()

	go func() {
		for {

			select {
			case <-ctx.Done():
				return
			case msg := <-r.out:
				if msg.Typ == messageTypeNodeUpdate {
					r.lastNode.Store(&msg.Node)
				}
				raw, err := json.Marshal(msg)
				if err != nil {
					panic("marshal overlay msg: " + err.Error())
				}

				sealed := r.SelfPriv.SealTo(r.PeerPriv.Public(), raw)
				// range over peers that have connected to us
				peers.Range(func(_, derpKey key.NodePublic) bool {
					fmt.Println("sending node to inbound peer")
					err = c.Send(derpKey, sealed)
					if err != nil {
						r.HumanLogf("Send updated node over DERP: %s", err)
						return false
					}
					return true
				})
				if selectedPeer := r.activePeer.Load(); selectedPeer != nil {
					// *selectedPeer <- &overlayMessage{
					// 	Typ:  messageTypeNodeUpdate,
					// 	Node: *msg.Node.Clone(),
					// }
					*selectedPeer <- msg
					fmt.Println("sending message")
				}
			}
		}
	}()

	for {
		msg, err := c.Recv()
		if err != nil {
			return err
		}

		switch msg := msg.(type) {
		case derp.ReceivedPacket:
			res, key, _, err := r.handleNextMessage(msg.Source, r.SelfPriv, r.PeerPriv.Public(), msg.Data)
			if err != nil {
				r.HumanLogf("Failed to handle overlay message: %s", err.Error())
				continue
			}

			if !key.IsZero() {
				peers.Store(key, msg.Source)
			}

			if res != nil {
				err = c.Send(msg.Source, res)
				if err != nil {
					r.HumanLogf("Failed to send overlay response over derp: %s", err.Error())
					return err
				}
			}
		}
	}
}

func (r *Wasm) newHelloPacket(ca ClientAuth, offer webrtc.SessionDescription) []byte {
	var (
		username string = r.username
		hostname string = "wush.dev"
	)

	raw, err := json.Marshal(overlayMessage{
		Typ: messageTypeHello,
		HostInfo: HostInfo{
			Username: username,
			Hostname: hostname,
		},
		Node:              *r.lastNode.Load(),
		WebrtcDescription: &offer,
	})
	if err != nil {
		panic("marshal node: " + err.Error())
	}

	sealed := ca.OverlayPrivateKey.SealTo(ca.ReceiverPublicKey, raw)
	return sealed
}

func (r *Wasm) handleNextMessage(derpPub key.NodePublic, selfPriv key.NodePrivate, peerPub key.NodePublic, msg []byte) (resRaw []byte, nodeKey key.NodePublic, _ overlayMessage, _ error) {
	cleartext, ok := selfPriv.OpenFrom(peerPub, msg)
	if !ok {
		return nil, key.NodePublic{}, overlayMessage{}, errors.New("message failed decryption")
	}

	var ovMsg overlayMessage
	fmt.Println(string(cleartext))
	err := json.Unmarshal(cleartext, &ovMsg)
	if err != nil {
		fmt.Printf("Unmarshal error: %#v\n", err)
		panic("unmarshal node: " + err.Error())
	}

	res := overlayMessage{}
	switch ovMsg.Typ {
	case messageTypePing:
		res.Typ = messageTypePong
	case messageTypePong:
		// do nothing
	case messageTypeHello:
		res.Typ = messageTypeHelloResponse
		res.HostInfo.Username = r.username
		res.HostInfo.Hostname = "wush.dev"
		username := "unknown"
		if u := ovMsg.HostInfo.Username; u != "" {
			username = u
		}
		hostname := "unknown"
		if h := ovMsg.HostInfo.Hostname; h != "" {
			hostname = h
		}
		if node := r.lastNode.Load(); node != nil {
			res.Node = *node
		}
		r.HumanLogf("%s Received connection request from %s", cliui.Timestamp(time.Now()), cliui.Keyword(fmt.Sprintf("%s@%s", username, hostname)))
		// TODO: impl
		r.onNewPeer.Invoke(map[string]any{
			"id":   js.ValueOf(derpPub.String()),
			"name": js.ValueOf("test"),
			"ip":   js.ValueOf("1.2.3.4"),
			"cancel": js.FuncOf(func(this js.Value, args []js.Value) any {
				return nil
			}),
		})

		if ovMsg.WebrtcDescription != nil {
			r.handleWebrtcOffer(derpPub, &res, *ovMsg.WebrtcDescription)
		}

	case messageTypeHelloResponse:
		if !ovMsg.Node.Key.IsZero() {
			r.in <- &ovMsg.Node
		}

		if ovMsg.WebrtcDescription != nil {
			r.onWebrtcAnswer.Invoke(js.ValueOf(derpPub.String()), map[string]any{
				"type": js.ValueOf(ovMsg.WebrtcDescription.Type.String()),
				"sdp":  js.ValueOf(ovMsg.WebrtcDescription.SDP),
			})
		}

	case messageTypeNodeUpdate:
		r.HumanLogf("%s Received updated node from %s", cliui.Timestamp(time.Now()), cliui.Code(ovMsg.Node.Key.String()))
		if !ovMsg.Node.Key.IsZero() {
			r.in <- &ovMsg.Node
		}

	case messageTypeWebRTCOffer:
		res.Typ = messageTypeWebRTCAnswer
		r.handleWebrtcOffer(derpPub, &res, *ovMsg.WebrtcDescription)

	case messageTypeWebRTCAnswer:
		r.onWebrtcAnswer.Invoke(js.ValueOf(derpPub.String()), js.ValueOf(map[string]any{
			"type": js.ValueOf(ovMsg.WebrtcDescription.Type.String()),
			"sdp":  js.ValueOf(ovMsg.WebrtcDescription.SDP),
		}))

	case messageTypeWebRTCCandidate:
		cand := map[string]any{
			"candidate": js.ValueOf(ovMsg.WebrtcCandidate.Candidate),
		}
		if ovMsg.WebrtcCandidate.SDPMLineIndex != nil {
			cand["sdpMLineIndex"] = js.ValueOf(int(*ovMsg.WebrtcCandidate.SDPMLineIndex))
		}
		if ovMsg.WebrtcCandidate.SDPMid != nil {
			cand["sdpMid"] = js.ValueOf(*ovMsg.WebrtcCandidate.SDPMid)
		}
		if ovMsg.WebrtcCandidate.UsernameFragment != nil {
			cand["usernameFragment"] = js.ValueOf(*ovMsg.WebrtcCandidate.UsernameFragment)
		}

		r.onWebrtcCandidate.Invoke(derpPub.String(), cand)

	}

	if res.Typ == 0 {
		return nil, ovMsg.Node.Key, ovMsg, nil
	}

	raw, err := json.Marshal(res)
	if err != nil {
		panic("marshal node: " + err.Error())
	}

	sealed := selfPriv.SealTo(peerPub, raw)
	return sealed, ovMsg.Node.Key, ovMsg, nil
}

func (r *Wasm) handleWebrtcOffer(derpPub key.NodePublic, res *overlayMessage, offer webrtc.SessionDescription) {
	wait := make(chan struct{})

	then := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		defer close(wait)
		desc := args[0]

		fmt.Printf("desc %#v\n", desc)
		res.WebrtcDescription = &webrtc.SessionDescription{}
		res.WebrtcDescription.Type = webrtc.NewSDPType(desc.Get("type").String())
		res.WebrtcDescription.SDP = desc.Get("sdp").String()

		return nil
	})
	defer then.Release()
	catch := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		defer close(wait)
		err := args[0]
		errStr := err.Call("toString").String()

		fmt.Println("rtc offer callback failed:", errStr)
		return nil
	})
	defer catch.Release()

	r.onWebrtcOffer.Invoke(js.ValueOf(derpPub.String()), map[string]any{
		"type": js.ValueOf(offer.Type.String()),
		"sdp":  js.ValueOf(offer.SDP),
	}).Call("then", then).Call("catch", catch)
	<-wait
}
