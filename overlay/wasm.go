//go:build js && wasm

package overlay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"sync"
	"sync/atomic"
	"syscall/js"
	"time"

	"github.com/coder/wush/cliui"
	"github.com/puzpuzpuz/xsync/v3"
	"tailscale.com/derp"
	"tailscale.com/derp/derphttp"
	"tailscale.com/net/netcheck"
	"tailscale.com/net/netmon"
	"tailscale.com/net/portmapper"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

func NewWasmOverlay(hlog Logf, dm *tailcfg.DERPMap, onNewPeer js.Value) *Wasm {
	return &Wasm{
		HumanLogf: hlog,
		DerpMap:   dm,
		SelfPriv:  key.NewNode(),
		PeerPriv:  key.NewNode(),
		SelfIP:    randv6(),

		peers:     xsync.NewMapOf[int32, chan<- *tailcfg.Node](),
		onNewPeer: onNewPeer,
		in:        make(chan *tailcfg.Node, 8),
		out:       make(chan *tailcfg.Node, 8),
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

	// peers is a map of channels that notify peers of node updates.
	peers     *xsync.MapOf[int32, chan<- *tailcfg.Node]
	onNewPeer js.Value

	lastNode atomic.Pointer[tailcfg.Node]
	// in funnels node updates from other peers to us
	in chan *tailcfg.Node
	// out fans out our node updates to peers
	out chan *tailcfg.Node
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
	} else {
		r.HumanLogf("Picked DERP region %s as overlay home", cliui.Code(r.DerpMap.Regions[report.PreferredDERP].RegionName))
		r.DerpRegionID = uint16(report.PreferredDERP)
	}

	return nil
}

func (r *Wasm) ClientAuth() *ClientAuth {
	return &ClientAuth{
		OverlayPrivateKey:    r.PeerPriv,
		ReceiverPublicKey:    r.SelfPriv.Public(),
		ReceiverDERPRegionID: r.DerpRegionID,
	}
}

func (r *Wasm) Recv() <-chan *tailcfg.Node {
	return r.in
}

func (r *Wasm) Send() chan<- *tailcfg.Node {
	return r.out
}

type Peer struct {
	ID   int32
	Name string
	IP   netip.Addr
}

func (r *Wasm) Connect(ctx context.Context, ca ClientAuth) (Peer, error) {
	derpPriv := key.NewNode()
	c := derphttp.NewRegionClient(derpPriv, func(format string, args ...any) {}, netmon.NewStatic(), func() *tailcfg.DERPRegion {
		return r.DerpMap.Regions[int(ca.ReceiverDERPRegionID)]
	})

	err := c.Connect(ctx)
	if err != nil {
		return Peer{}, err
	}

	sealed := r.newHelloPacket(ca)
	err = c.Send(ca.ReceiverPublicKey, sealed)
	if err != nil {
		return Peer{}, fmt.Errorf("send overlay hello over derp: %w", err)
	}

	updates := make(chan *tailcfg.Node, 2)

	peerID := rand.Int32()
	r.peers.Store(peerID, updates)

	go func() {
		defer r.peers.Delete(peerID)

		for {
			select {
			case <-ctx.Done():
				return
			case node := <-updates:
				msg := overlayMessage{
					Typ:  messageTypeNodeUpdate,
					Node: *node,
				}
				raw, err := json.Marshal(msg)
				if err != nil {
					panic("marshal node: " + err.Error())
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

				res, _, ovmsg, err := r.handleNextMessage(ca.OverlayPrivateKey, ca.ReceiverPublicKey, msg.Data)
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
					closeOnce.Do(func() {
						close(waitHello)
					})
				}
			}
		}
	}()

	select {
	case <-time.After(10 * time.Second):
		return Peer{}, errors.New("timed out waiting for peer to respond")
	case <-waitHello:
		updates <- r.lastNode.Load()
		if len(helloResp.Node.Addresses) == 0 {
			return Peer{}, fmt.Errorf("peer has no addresses")
		}
		ip := helloResp.Node.Addresses[0].Addr()
		return Peer{
			ID:   peerID,
			IP:   ip,
			Name: helloResp.HostInfo.Username,
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

	// node priv -> derp priv
	peers := xsync.NewMapOf[key.NodePublic, key.NodePublic]()

	go func() {
		for {

			select {
			case <-ctx.Done():
				return
			case node := <-r.out:
				r.lastNode.Store(node)
				raw, err := json.Marshal(overlayMessage{
					Typ:  messageTypeNodeUpdate,
					Node: *node,
				})
				if err != nil {
					panic("marshal node: " + err.Error())
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
				// range over peers that we have connected to
				r.peers.Range(func(key int32, value chan<- *tailcfg.Node) bool {
					fmt.Println("sending node to outbound peer")
					value <- node.Clone()
					return true
				})
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
			res, key, _, err := r.handleNextMessage(r.SelfPriv, r.PeerPriv.Public(), msg.Data)
			if err != nil {
				r.HumanLogf("Failed to handle overlay message: %s", err.Error())
				continue
			}

			peers.Store(key, msg.Source)

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

func (r *Wasm) newHelloPacket(ca ClientAuth) []byte {
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
		Node: *r.lastNode.Load(),
	})
	if err != nil {
		panic("marshal node: " + err.Error())
	}

	sealed := ca.OverlayPrivateKey.SealTo(ca.ReceiverPublicKey, raw)
	return sealed
}

func (r *Wasm) handleNextMessage(selfPriv key.NodePrivate, peerPub key.NodePublic, msg []byte) (resRaw []byte, nodeKey key.NodePublic, _ overlayMessage, _ error) {
	cleartext, ok := selfPriv.OpenFrom(peerPub, msg)
	if !ok {
		return nil, key.NodePublic{}, overlayMessage{}, errors.New("message failed decryption")
	}

	var ovMsg overlayMessage
	err := json.Unmarshal(cleartext, &ovMsg)
	if err != nil {
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
			"id":   js.ValueOf(0),
			"name": js.ValueOf(""),
			"ip":   js.ValueOf(""),
			"cancel": js.FuncOf(func(this js.Value, args []js.Value) any {
				return nil
			}),
		})
	case messageTypeHelloResponse:
		if !ovMsg.Node.Key.IsZero() {
			r.in <- &ovMsg.Node
		}
	case messageTypeNodeUpdate:
		r.HumanLogf("%s Received updated node from %s", cliui.Timestamp(time.Now()), cliui.Code(ovMsg.Node.Key.String()))
		if !ovMsg.Node.Key.IsZero() {
			r.in <- &ovMsg.Node
		}
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
