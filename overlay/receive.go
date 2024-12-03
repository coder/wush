//go:build !js && !wasm
// +build !js,!wasm

package overlay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/stun/v3"
	"github.com/pion/webrtc/v4"
	"github.com/puzpuzpuz/xsync/v3"
	"github.com/schollz/progressbar/v3"
	"tailscale.com/derp"
	"tailscale.com/derp/derphttp"
	"tailscale.com/net/netcheck"
	"tailscale.com/net/netmon"
	"tailscale.com/net/portmapper"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"

	"github.com/coder/pretty"
	"github.com/coder/wush/cliui"
)

func NewReceiveOverlay(logger *slog.Logger, hlog Logf, dm *tailcfg.DERPMap) *Receive {
	return &Receive{
		Logger:      logger,
		HumanLogf:   hlog,
		DerpMap:     dm,
		SelfPriv:    key.NewNode(),
		PeerPriv:    key.NewNode(),
		webrtcConns: xsync.NewMapOf[key.NodePublic, *webrtc.PeerConnection](),
		in:          make(chan *tailcfg.Node, 8),
		out:         make(chan *overlayMessage, 8),
	}
}

type Receive struct {
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

	// stunIP is the STUN address that can be used for P2P overlay
	// communication.
	stunIP netip.AddrPort
	// derpRegionID is the DERP region that can be used for proxied overlay
	// communication.
	derpRegionID uint16

	webrtcConns *xsync.MapOf[key.NodePublic, *webrtc.PeerConnection]

	lastNode atomic.Pointer[tailcfg.Node]
	// in funnels node updates from other peers to us
	in chan *tailcfg.Node
	// out fans out our node updates to peers
	out chan *overlayMessage
}

func (r *Receive) IPs() []netip.Addr {
	i6 := [16]byte{0xfd, 0x7a, 0x11, 0x5c, 0xa1, 0xe0}
	i6[15] = 0x01
	return []netip.Addr{
		// netip.AddrFrom4([4]byte{100, 64, 0, 0}),
		netip.AddrFrom16(i6),
	}
}

var webrtcConfig = webrtc.Configuration{
	ICEServers: []webrtc.ICEServer{
		{
			URLs: []string{"stun:stun.l.google.com:19302"},
		},
	},
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
		r.HumanLogf("Failed to determine overlay DERP region, defaulting to %s.", cliui.Code("NYC"))
		r.derpRegionID = 1
	} else {
		r.HumanLogf("Picked DERP region %s as overlay home", cliui.Code(r.DerpMap.Regions[report.PreferredDERP].RegionName))
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

func (r *Receive) SendTailscaleNodeUpdate(node *tailcfg.Node) {
	r.out <- &overlayMessage{
		Typ:  messageTypeNodeUpdate,
		Node: *node.Clone(),
	}
}

// gonna have to do something special for per-peer webrtc connections

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
					r.HumanLogf("%s Failed to write STUN request on overlay: %s", cliui.Timestamp(time.Now()), err)
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
			case msg := <-r.out:
				if msg.Typ == messageTypeNodeUpdate {
					r.lastNode.Store(&msg.Node)
				}
				raw, err := json.Marshal(msg)
				if err != nil {
					panic("marshal overlay msg: " + err.Error())
				}

				sealed := r.SelfPriv.SealTo(r.PeerPriv.Public(), raw)
				peers.Range(func(_ key.NodePublic, addr netip.AddrPort) bool {
					_, err := conn.WriteToUDPAddrPort(sealed, addr)
					if err != nil {
						r.HumanLogf("%s Failed to send updated node over udp: %s", cliui.Timestamp(time.Now()), err)
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
					r.HumanLogf("STUN address is %s", cliui.Code(stunAddrPort.String()))
				}

				if r.stunIP.IsValid() && r.stunIP.Compare(stunAddrPort) != 0 {
					r.HumanLogf(pretty.Sprintf(cliui.DefaultStyles.Warn, "STUN address changed, this may cause issues; %s->%s", r.stunIP.String(), stunAddrPort.String()))
				}
				r.stunIP = stunAddrPort
				closeIPChanOnce.Do(func() {
					close(ipChan)
				})
				continue
			}

			res, key, err := r.handleNextMessage(key.NodePublic{}, buf, "STUN")
			if err != nil {
				r.HumanLogf("Failed to handle overlay message: %s", err.Error())
				continue
			}

			peers.Store(key, addr)

			if res != nil {
				_, err = conn.WriteToUDPAddrPort(res, addr)
				if err != nil {
					r.HumanLogf("Failed to send overlay response over STUN: %s", err.Error())
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
			case msg := <-r.out:
				if msg.Typ == messageTypeNodeUpdate {
					r.lastNode.Store(&msg.Node)
				}
				raw, err := json.Marshal(msg)
				if err != nil {
					panic("marshal overlay msg: " + err.Error())
				}

				sealed := r.SelfPriv.SealTo(r.PeerPriv.Public(), raw)
				peers.Range(func(_, derpKey key.NodePublic) bool {
					err = c.Send(derpKey, sealed)
					if err != nil {
						r.HumanLogf("Send updated node over DERP: %s", err)
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
			res, key, err := r.handleNextMessage(msg.Source, msg.Data, "DERP")
			if err != nil {
				r.HumanLogf("Failed to handle overlay message from %s: %s", msg.Source.ShortString(), err.Error())
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

func (r *Receive) handleNextMessage(src key.NodePublic, msg []byte, system string) (resRaw []byte, nodeKey key.NodePublic, _ error) {
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
		username := "unknown"
		if u := ovMsg.HostInfo.Username; u != "" {
			username = u
		}
		hostname := "unknown"
		if h := ovMsg.HostInfo.Hostname; h != "" {
			hostname = h
		}
		if lastNode := r.lastNode.Load(); lastNode != nil {
			res.Node = *lastNode
		}

		if ovMsg.WebrtcDescription != nil {
			r.setupWebrtcConnection(src, &res, *ovMsg.WebrtcDescription)
		}

		r.HumanLogf("%s Received connection request over %s from %s", cliui.Timestamp(time.Now()), system, cliui.Keyword(fmt.Sprintf("%s@%s", username, hostname)))
	case messageTypeNodeUpdate:
		r.Logger.Debug("received updated node", slog.String("node_key", ovMsg.Node.Key.String()))
		r.in <- &ovMsg.Node
		res.Typ = messageTypeNodeUpdate
		if lastNode := r.lastNode.Load(); lastNode != nil {
			res.Node = *lastNode
		}

	case messageTypeWebRTCCandidate:
		pc, ok := r.webrtcConns.Load(src)
		if !ok {
			fmt.Println("got candidate for unknown connection")
			break
		}

		err := pc.AddICECandidate(*ovMsg.WebrtcCandidate)
		if err != nil {
			fmt.Println("failed to add ice candidate:", err)
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

func (r *Receive) setupWebrtcConnection(src key.NodePublic, res *overlayMessage, offer webrtc.SessionDescription) {
	// Configure larger buffer sizes
	settingEngine := webrtc.SettingEngine{}
	// Set maximum message size to 16MB
	settingEngine.SetSCTPMaxReceiveBufferSize(64 * 1024 * 1024)

	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))

	// Use the custom API to create the peer connection
	peerConnection, err := api.NewPeerConnection(webrtcConfig)
	if err != nil {
		panic(err)
	}

	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		switch s {
		case webrtc.PeerConnectionStateConnected:
		case webrtc.PeerConnectionStateDisconnected:
		case webrtc.PeerConnectionStateFailed:
		case webrtc.PeerConnectionStateClosed:
		}
	})

	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		// Increase buffer sizes
		d.SetBufferedAmountLowThreshold(65535)

		var (
			fi     *os.File
			bar    *progressbar.ProgressBar
			mw     io.Writer
			fiSize int
			read   int
		)

		d.OnMessage(func(msg webrtc.DataChannelMessage) {
			if msg.IsString {
				meta := RtcMetadata{}

				err := json.Unmarshal(msg.Data, &meta)
				if err != nil {
					fmt.Println("failed to unmarshal file metadata:")
					d.Close()
					return
				}

				if meta.Type == RtcMetadataTypeFileMetadata {
					fiSize = meta.FileMetadata.FileSize
					fi, err = os.OpenFile(meta.FileMetadata.FileName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
					if err != nil {
						fmt.Println("failed to open file", err)
					}

					bar = progressbar.DefaultBytes(
						int64(fiSize),
						fmt.Sprintf("Downloading %q", meta.FileMetadata.FileName),
					)
					mw = io.MultiWriter(fi, bar)
				}

			} else {
				read += len(msg.Data)
				if fi == nil {
					fmt.Println("Error: Received binary data before file was opened")
					d.Close()
					return
				}

				_, err := mw.Write(msg.Data)
				if err != nil {
					fmt.Printf("Failed to write file data: %v\n", err)
					d.Close()
					return
				}

				if read >= fiSize {
					bar.Close()
					fmt.Printf("Successfully wrote file %s (%d bytes)\n", fi.Name(), read)
					err := fi.Close()
					if err != nil {
						fmt.Printf("Error closing file: %v\n", err)
					}
					fi = nil
					bar = nil
					mw = nil
				}
			}
		})
	})

	peerConnection.OnICECandidate(func(i *webrtc.ICECandidate) {
		if i == nil {
			return
		}
		ic := i.ToJSON()

		r.out <- &overlayMessage{
			Typ:             messageTypeWebRTCCandidate,
			WebrtcCandidate: &ic,
		}
	})

	err = peerConnection.SetRemoteDescription(offer)
	if err != nil {
		fmt.Println("failed to set remote description:", err)
	}

	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		fmt.Println("failed to create answer:", err)
	}

	err = peerConnection.SetLocalDescription(answer)
	if err != nil {
		fmt.Println("failed to set local description:", err)
	}

	res.WebrtcDescription = &answer

	r.webrtcConns.Store(src, peerConnection)
}
