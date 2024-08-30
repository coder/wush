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
	"sync"
	"time"

	"github.com/coder/wush/cliui"
	"tailscale.com/derp"
	"tailscale.com/derp/derphttp"
	"tailscale.com/net/netmon"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

func NewSendOverlay(logger *slog.Logger, dm *tailcfg.DERPMap) *Send {
	return &Send{
		derpMap: dm,
		in:      make(chan *tailcfg.Node, 8),
		out:     make(chan *tailcfg.Node, 8),
		waitIP:  make(chan struct{}),
	}
}

type Send struct {
	Logger         *slog.Logger
	STUNIPOverride netip.Addr
	derpMap        *tailcfg.DERPMap

	// _ip is the ip we get from the receiver, which is our ip on the tailnet.
	_ip        netip.Addr
	waitIP     chan struct{}
	waitIPOnce sync.Once

	Auth ClientAuth

	in  chan *tailcfg.Node
	out chan *tailcfg.Node
}

func (s *Send) IP() netip.Addr {
	<-s.waitIP
	return s._ip
}

func (s *Send) Recv() <-chan *tailcfg.Node {
	return s.in
}

func (s *Send) Send() chan<- *tailcfg.Node {
	return s.out
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
			case node := <-s.out:
				msg := overlayMessage{
					Typ:  messageTypeNodeUpdate,
					Node: *node,
				}
				raw, err := json.Marshal(msg)
				if err != nil {
					panic("marshal node: " + err.Error())
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
		_ = addr
		fmt.Println("new UDP msg from", addr.String())

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
			case node := <-s.out:
				msg := overlayMessage{
					Typ:  messageTypeNodeUpdate,
					Node: *node,
				}
				raw, err := json.Marshal(msg)
				if err != nil {
					panic("marshal node: " + err.Error())
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
	})
	if err != nil {
		panic("marshal node: " + err.Error())
	}

	fmt.Println("sending", string(raw))
	sealed := s.Auth.OverlayPrivateKey.SealTo(s.Auth.ReceiverPublicKey, raw)
	return sealed
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
		s._ip = ovMsg.IP
		s.waitIPOnce.Do(func() {
			close(s.waitIP)
		})
		// fmt.Println("Received IP from peer:", s._ip.String())
	case messageTypeNodeUpdate:
		s.in <- &ovMsg.Node
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
