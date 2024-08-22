package overlay

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/stun/v3"
	"github.com/puzpuzpuz/xsync/v3"
	"tailscale.com/derp"
	"tailscale.com/derp/derphttp"
	"tailscale.com/net/netcheck"
	"tailscale.com/net/netmon"
	"tailscale.com/net/portmapper"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"

	"github.com/coder/wush/cliui"
)

func NewReceiveOverlay(logger *slog.Logger, dm *tailcfg.DERPMap) *Receive {
	return &Receive{
		Logger:   logger,
		DerpMap:  dm,
		SelfPriv: key.NewNode(),
		PeerPriv: key.NewNode(),
		in:       make(chan *tailcfg.Node, 8),
		out:      make(chan *tailcfg.Node, 8),
	}
}

type Receive struct {
	Logger  *slog.Logger
	DerpMap *tailcfg.DERPMap
	// SelfPriv is the private key that peers will encrypt overlay messages to.
	// The public key of this is sent in the auth key.
	SelfPriv key.NodePrivate
	// PeerPriv is the main auth mechanism used to secure the overlay. Peers are
	// sent this private key to encrypt node communication. Leaking this private
	// key would allow anyone to connect.
	PeerPriv key.NodePrivate

	// stunIP is the STUN address that can be used for P2P overlay
	// communication.
	stunIP netip.AddrPort
	// derpRegionID is the DERP region that can be used for proxied overlay
	// communication.
	derpRegionID uint16

	// nextPeerIP is a counter that assigns IP addresses to new peers in
	// ascending order. It contains the last two bytes of an IPv4 address,
	// 100.64.x.x.
	nextPeerIP uint16

	lastNode atomic.Pointer[tailcfg.Node]
	in       chan *tailcfg.Node
	out      chan *tailcfg.Node
}

func (r *Receive) IP() netip.Addr {
	return netip.AddrFrom4([4]byte{100, 64, 0, 0})
}

func (r *Receive) PickDERPHome(ctx context.Context) error {
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
		fmt.Println("Failed to determine overlay DERP region, defaulting to", cliui.Code("NYC"), ".")
		r.derpRegionID = 1
	} else {
		fmt.Println("Picked DERP region", cliui.Code(r.DerpMap.Regions[report.PreferredDERP].RegionName), "as overlay home")
		r.derpRegionID = uint16(report.PreferredDERP)
	}

	return nil
}

func (r *Receive) ClientAuth() *ClientAuth {
	return &ClientAuth{
		OverlayPrivateKey:    r.PeerPriv,
		ReceiverPublicKey:    r.SelfPriv.Public(),
		ReceiverStunAddr:     r.stunIP,
		ReceiverDERPRegionID: r.derpRegionID,
	}
}

func (r *Receive) Recv() <-chan *tailcfg.Node {
	return r.in
}

func (r *Receive) Send() chan<- *tailcfg.Node {
	return r.out
}

func (r *Receive) ListenOverlaySTUN(ctx context.Context) (<-chan struct{}, error) {
	srvAddr, err := net.ResolveUDPAddr("udp4", "stun.l.google.com:19302")
	if err != nil {
		return nil, fmt.Errorf("resolve google STUN: %w", err)
	}

	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return nil, fmt.Errorf("listen STUN: %w", err)
	}

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	m := stun.MustBuild(stun.TransactionID, stun.BindingRequest)

	restun := time.NewTicker(time.Nanosecond)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return

			case <-restun.C:
				_, err = conn.WriteToUDP(m.Raw, srvAddr)
				if err != nil {
					fmt.Println(cliui.Timestamp(time.Now()), "Failed to write STUN request on overlay:", err)
				}
				restun.Reset(30 * time.Second)
			}
		}
	}()

	// node priv -> udp addr
	peers := xsync.NewMapOf[key.NodePublic, netip.AddrPort]()

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
				peers.Range(func(_ key.NodePublic, addr netip.AddrPort) bool {
					_, err := conn.WriteToUDPAddrPort(sealed, addr)
					if err != nil {
						fmt.Println("send response over udp:", err)
						return false
					}
					return true
				})
			}
		}
	}()

	ipChan := make(chan struct{})

	go func() {
		var closeIPChanOnce sync.Once

		for {
			buf := make([]byte, 4<<10)
			n, addr, err := conn.ReadFromUDPAddrPort(buf)
			if err != nil {
				r.Logger.Error("read from STUN; exiting", "err", err)
				return
			}

			buf = buf[:n]
			if stun.IsMessage(buf) {
				m := new(stun.Message)
				m.Raw = buf

				if err := m.Decode(); err != nil {
					r.Logger.Error("decode STUN message; exiting", "err", err)
					return
				}

				var xorAddr stun.XORMappedAddress
				if err := xorAddr.GetFrom(m); err != nil {
					r.Logger.Error("decode STUN xor mapped addr; exiting", "err", err)
					return
				}

				stunAddr, ok := netip.AddrFromSlice(xorAddr.IP)
				if !ok {
					r.Logger.Error("convert STUN xor mapped addr", "ip", xorAddr.IP.String())
					continue
				}
				stunAddrPort := netip.AddrPortFrom(stunAddr, uint16(xorAddr.Port))

				// our first STUN response
				if !r.stunIP.IsValid() {
					fmt.Println(cliui.Timestamp(time.Now()), "STUN address is", cliui.Code(stunAddrPort.String()))
				}

				if r.stunIP.IsValid() && r.stunIP.Compare(stunAddrPort) != 0 {
					r.Logger.Warn("STUN address changed, this may cause issues",
						"old_ip", r.stunIP.String(),
						"new_ip", stunAddrPort.String(),
					)
				}
				r.stunIP = stunAddrPort
				closeIPChanOnce.Do(func() {
					close(ipChan)
				})
				continue
			}

			res, key, err := r.handleNextMessage(buf, "STUN")
			if err != nil {
				fmt.Println(cliui.Timestamp(time.Now()), "Failed to handle overlay message:", err.Error())
				continue
			}

			peers.Store(key, addr)

			if res != nil {
				_, err = conn.WriteToUDPAddrPort(res, addr)
				if err != nil {
					fmt.Println(cliui.Timestamp(time.Now()), "Failed to send overlay response over STUN:", err.Error())
					return
				}
			}
		}
	}()
	return ipChan, nil
}

func (r *Receive) ListenOverlayDERP(ctx context.Context) error {
	c := derphttp.NewRegionClient(r.SelfPriv, func(format string, args ...any) {}, netmon.NewStatic(), func() *tailcfg.DERPRegion {
		return r.DerpMap.Regions[int(r.derpRegionID)]
	})

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
				peers.Range(func(_, derpKey key.NodePublic) bool {
					err = c.Send(derpKey, sealed)
					if err != nil {
						fmt.Println("send response over derp:", err)
						return false
					}
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
			res, key, err := r.handleNextMessage(msg.Data, "DERP")
			if err != nil {
				fmt.Println(cliui.Timestamp(time.Now()), "Failed to handle overlay message:", err.Error())
				continue
			}

			peers.Store(key, msg.Source)

			if res != nil {
				err = c.Send(msg.Source, res)
				if err != nil {
					fmt.Println(cliui.Timestamp(time.Now()), "Failed to send overlay response over derp:", err.Error())
					return err
				}
			}
		}
	}
}

func (r *Receive) handleNextMessage(msg []byte, system string) (resRaw []byte, nodeKey key.NodePublic, _ error) {
	cleartext, ok := r.SelfPriv.OpenFrom(r.PeerPriv.Public(), msg)
	if !ok {
		return nil, key.NodePublic{}, errors.New("message failed decryption")
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
		res.IP = r.assignNextIP()
		fmt.Println(cliui.Timestamp(time.Now()), "Received connection request over", system)
	case messageTypeNodeUpdate:
		fmt.Println(cliui.Timestamp(time.Now()), "Received updated node from", cliui.Code(ovMsg.Node.Key.String()))
		r.in <- &ovMsg.Node
		res.Typ = messageTypeNodeUpdate
		if lastNode := r.lastNode.Load(); lastNode != nil {
			res.Node = *lastNode
		}
	}

	if res.Typ == 0 {
		return nil, ovMsg.Node.Key, nil
	}

	raw, err := json.Marshal(res)
	if err != nil {
		panic("marshal node: " + err.Error())
	}

	sealed := r.SelfPriv.SealTo(r.PeerPriv.Public(), raw)
	return sealed, ovMsg.Node.Key, nil
}

func (r *Receive) assignNextIP() netip.Addr {
	r.nextPeerIP += 1

	addrBytes := [4]byte{100, 64, 0, 0}
	binary.BigEndian.PutUint16(addrBytes[2:], r.nextPeerIP)

	return netip.AddrFrom4(addrBytes)
}
