//go:build !js && !wasm
// +build !js,!wasm

package overlay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/user"
	"time"

	"github.com/coder/wush/cliui"
	"github.com/pion/webrtc/v4"
	"tailscale.com/derp"
	"tailscale.com/derp/derphttp"
	"tailscale.com/net/netmon"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

func NewSendOverlay(logger *slog.Logger, dm *tailcfg.DERPMap) *Send {
	s := &Send{
		derpMap:          dm,
		in:               make(chan *tailcfg.Node, 8),
		out:              make(chan *overlayMessage, 8),
		waitIce:          make(chan struct{}),
		WaitTransferDone: make(chan struct{}),
		SelfIP:           randv6(),
	}
	s.setupWebrtcConnection()
	return s
}

type Send struct {
	Logger         *slog.Logger
	STUNIPOverride netip.Addr
	derpMap        *tailcfg.DERPMap

	SelfIP netip.Addr

	Auth ClientAuth

	RtcConn          *webrtc.PeerConnection
	RtcDc            *webrtc.DataChannel
	offer            webrtc.SessionDescription
	waitIce          chan struct{}
	WaitTransferDone chan struct{}

	in  chan *tailcfg.Node
	out chan *overlayMessage
}

func (s *Send) IPs() []netip.Addr {
	return []netip.Addr{s.SelfIP}
}

func (s *Send) Recv() <-chan *tailcfg.Node {
	return s.in
}

func (s *Send) SendTailscaleNodeUpdate(node *tailcfg.Node) {
	s.out <- &overlayMessage{
		Typ:  messageTypeNodeUpdate,
		Node: *node.Clone(),
	}
}

func (s *Send) ListenOverlaySTUN(ctx context.Context) error {
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return fmt.Errorf("listen STUN: %w", err)
	}

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	sealed := s.newHelloPacket()
	receiverAddr := s.Auth.ReceiverStunAddr
	if s.STUNIPOverride.IsValid() {
		receiverAddr = netip.AddrPortFrom(s.STUNIPOverride, s.Auth.ReceiverStunAddr.Port())
	}

	_, err = conn.WriteToUDPAddrPort(sealed, receiverAddr)
	if err != nil {
		return fmt.Errorf("send overlay hello over STUN: %w", err)
	}

	keepAlive := time.NewTicker(30 * time.Second)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-s.out:
				raw, err := json.Marshal(msg)
				if err != nil {
					panic("marshal overlay msg: " + err.Error())
				}

				sealed := s.Auth.OverlayPrivateKey.SealTo(s.Auth.ReceiverPublicKey, raw)
				_, err = conn.WriteToUDPAddrPort(sealed, receiverAddr)
				if err != nil {
					fmt.Printf("send response over STUN: %s\n", err)
					return
				}

			case <-keepAlive.C:
				msg := overlayMessage{
					Typ: messageTypePing,
				}
				raw, err := json.Marshal(msg)
				if err != nil {
					panic("marshal node: " + err.Error())
				}

				sealed := s.Auth.OverlayPrivateKey.SealTo(s.Auth.ReceiverPublicKey, raw)
				_, err = conn.WriteToUDPAddrPort(sealed, receiverAddr)
				if err != nil {
					fmt.Printf("send ping message over STUN: %s\n", err)
					return
				}
			}
		}
	}()

	for {
		buf := make([]byte, 4<<10)
		n, addr, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			s.Logger.Error("read from STUN; exiting", "err", err)
			return err
		}

		buf = buf[:n]

		res, err := s.handleNextMessage(buf)
		if err != nil {
			fmt.Println(cliui.Timestamp(time.Now()), "Failed to handle overlay message:", err.Error())
			continue
		}

		if res != nil {
			_, err = conn.WriteToUDPAddrPort(res, addr)
			if err != nil {
				fmt.Println(cliui.Timestamp(time.Now()), "Failed to send overlay response over STUN:", err.Error())
				return err
			}
		}
	}
}

func (s *Send) ListenOverlayDERP(ctx context.Context) error {
	derpPriv := key.NewNode()
	c := derphttp.NewRegionClient(derpPriv, func(format string, args ...any) {}, netmon.NewStatic(), func() *tailcfg.DERPRegion {
		return s.derpMap.Regions[int(s.Auth.ReceiverDERPRegionID)]
	})

	err := c.Connect(ctx)
	if err != nil {
		return err
	}

	sealed := s.newHelloPacket()
	err = c.Send(s.Auth.ReceiverPublicKey, sealed)
	if err != nil {
		return fmt.Errorf("send overlay hello over derp: %w", err)
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-s.out:
				raw, err := json.Marshal(msg)
				if err != nil {
					panic("marshal overlay msg: " + err.Error())
				}

				sealed := s.Auth.OverlayPrivateKey.SealTo(s.Auth.ReceiverPublicKey, raw)
				err = c.Send(s.Auth.ReceiverPublicKey, sealed)
				if err != nil {
					fmt.Printf("send response over derp: %s\n", err)
					return
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
			if s.Auth.ReceiverPublicKey != msg.Source {
				fmt.Printf("message from unknown peer %s\n", msg.Source.String())
				continue
			}

			res, err := s.handleNextMessage(msg.Data)
			if err != nil {
				fmt.Println("Failed to handle overlay message", err)
				continue
			}

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

func (s *Send) newHelloPacket() []byte {
	var (
		username,
		hostname string
	)

	cu, _ := user.Current()
	if cu != nil {
		username = cu.Username
	}
	hostname, _ = os.Hostname()

	raw, err := json.Marshal(overlayMessage{
		Typ: messageTypeHello,
		HostInfo: HostInfo{
			Username: username,
			Hostname: hostname,
		},
		WebrtcDescription: &s.offer,
	})
	if err != nil {
		panic("marshal node: " + err.Error())
	}

	sealed := s.Auth.OverlayPrivateKey.SealTo(s.Auth.ReceiverPublicKey, raw)
	return sealed
}

const (
	RtcMetadataTypeFileMetadata = "file_metadata"
	RtcMetadataTypeFileComplete = "file_complete"
	RtcMetadataTypeFileAck      = "file_ack"
)

type RtcMetadata struct {
	Type         string          `json:"type"`
	FileMetadata RtcFileMetadata `json:"fileMetadata"`
}
type RtcFileMetadata struct {
	FileName string `json:"fileName"`
	FileSize int    `json:"fileSize"`
}

func (s *Send) handleNextMessage(msg []byte) (resRaw []byte, _ error) {
	cleartext, ok := s.Auth.OverlayPrivateKey.OpenFrom(s.Auth.ReceiverPublicKey, msg)
	if !ok {
		return nil, errors.New("message failed decryption")
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
	case messageTypeHelloResponse:
		s.in <- &ovMsg.Node
		close(s.waitIce)
		s.RtcConn.SetRemoteDescription(*ovMsg.WebrtcDescription)
	case messageTypeNodeUpdate:
		s.in <- &ovMsg.Node
	case messageTypeWebRTCCandidate:
		s.RtcConn.AddICECandidate(*ovMsg.WebrtcCandidate)
	}

	if res.Typ == 0 {
		return nil, nil
	}

	raw, err := json.Marshal(res)
	if err != nil {
		panic("marshal node: " + err.Error())
	}

	sealed := s.Auth.OverlayPrivateKey.SealTo(s.Auth.ReceiverPublicKey, raw)
	return sealed, nil
}

func (s *Send) setupWebrtcConnection() {
	var err error
	s.RtcConn, err = webrtc.NewPeerConnection(getWebRTCConfig())
	if err != nil {
		panic("failed to create webrtc connection: " + err.Error())
	}

	s.RtcConn.OnICECandidate(func(i *webrtc.ICECandidate) {
		if i == nil {
			return
		}
		ic := i.ToJSON()

		<-s.waitIce
		s.out <- &overlayMessage{
			Typ:             messageTypeWebRTCCandidate,
			WebrtcCandidate: &ic,
		}
	})

	s.RtcDc, err = s.RtcConn.CreateDataChannel("fileTransfer", nil)
	if err != nil {
		fmt.Println("failed to create dc:", err)
	}

	// Add message handler to our created data channel
	s.RtcDc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if msg.IsString {
			meta := RtcMetadata{}

			err := json.Unmarshal(msg.Data, &meta)
			if err != nil {
				fmt.Println("failed to unmarshal metadata:", err)
				return
			}

			if meta.Type == RtcMetadataTypeFileAck {
				close(s.WaitTransferDone)
				return
			}
			return
		}
	})

	answer, err := s.RtcConn.CreateOffer(nil)
	if err != nil {
		fmt.Println("failed to create answer:", err)
	}

	err = s.RtcConn.SetLocalDescription(answer)
	if err != nil {
		fmt.Println("failed to set local description:", err)
	}

	s.offer = answer
}
