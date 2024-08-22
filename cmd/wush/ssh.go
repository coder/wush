package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"time"

	"github.com/charmbracelet/huh"
	"tailscale.com/client/tailscale"
	"tailscale.com/net/netns"
	"tailscale.com/tailcfg"

	"github.com/coder/serpent"
	"github.com/coder/wush/cliui"
	"github.com/coder/wush/overlay"
	"github.com/coder/wush/tsserver"
	xssh "github.com/coder/wush/xssh"
)

func sshCmd() *serpent.Command {
	var (
		authID             string
		waitP2P            bool
		overlayTransport   string
		stunAddrOverride   string
		stunAddrOverrideIP netip.Addr
		sshStdio           bool
	)
	return &serpent.Command{
		Use: "wush",
		Long: "Opens an SSH connection to a " + cliui.Code("wush") + " peer. " +
			"Use " + cliui.Code("wush receive") + " on the computer you would like to connect to.",
		Handler: func(inv *serpent.Invocation) error {
			ctx := inv.Context()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			if authID == "" {
				err := huh.NewInput().
					Title("Enter the receiver's Auth ID:").
					Value(&authID).
					Run()
				if err != nil {
					return fmt.Errorf("get auth id: %w", err)
				}
			}

			dm, err := tsserver.DERPMapTailscale(ctx)
			if err != nil {
				return err
			}

			if stunAddrOverride != "" {
				stunAddrOverrideIP, err = netip.ParseAddr(stunAddrOverride)
				if err != nil {
					return fmt.Errorf("parse stun addr override: %w", err)
				}
			}

			send := overlay.NewSendOverlay(logger, dm)
			send.STUNIPOverride = stunAddrOverrideIP

			err = send.Auth.Parse(authID)
			if err != nil {
				return fmt.Errorf("parse auth key: %w", err)
			}

			if !sshStdio {
				fmt.Println("Auth information:")
				stunStr := send.Auth.ReceiverStunAddr.String()
				if !send.Auth.ReceiverStunAddr.IsValid() {
					stunStr = "Disabled"
				}
				fmt.Println("\t> Server overlay STUN address:", cliui.Code(stunStr))
				derpStr := "Disabled"
				if send.Auth.ReceiverDERPRegionID > 0 {
					derpStr = dm.Regions[int(send.Auth.ReceiverDERPRegionID)].RegionName
				}
				fmt.Println("\t> Server overlay DERP home:   ", cliui.Code(derpStr))
				fmt.Println("\t> Server overlay public key:  ", cliui.Code(send.Auth.ReceiverPublicKey.ShortString()))
				fmt.Println("\t> Server overlay auth key:    ", cliui.Code(send.Auth.OverlayPrivateKey.Public().ShortString()))
			}

			s, err := tsserver.NewServer(ctx, logger, send)
			if err != nil {
				return err
			}

			switch overlayTransport {
			case "derp":
				if send.Auth.ReceiverDERPRegionID == 0 {
					return errors.New("overlay type is \"derp\", but receiver is of type \"stun\"")
				}
				go send.ListenOverlayDERP(ctx)
			case "stun":
				if !send.Auth.ReceiverStunAddr.IsValid() {
					return errors.New("overlay type is \"stun\", but receiver is of type \"derp\"")
				}
				go send.ListenOverlaySTUN(ctx)
			}

			go s.ListenAndServe(ctx)
			netns.SetDialerOverride(s.Dialer())
			ts, err := newTSNet("send")
			if err != nil {
				return err
			}
			ts.Logf = func(string, ...any) {}
			ts.UserLogf = func(string, ...any) {}

			// fmt.Println("Bringing Wireguard up..")
			ts.Up(ctx)
			// fmt.Println("Wireguard is ready!")

			lc, err := ts.LocalClient()
			if err != nil {
				return err
			}

			ip, err := waitUntilHasPeerHasIP(ctx, lc)
			if err != nil {
				return err
			}

			if waitP2P {
				err := waitUntilHasP2P(ctx, lc)
				if err != nil {
					return err
				}
			}

			return xssh.TailnetSSH(ctx, inv, ts, ip.String()+":3", sshStdio)
		},
		Options: []serpent.Option{
			{
				Flag:        "auth-id",
				Env:         "WUSH_AUTH_ID",
				Description: "The auth id returned by " + cliui.Code("wush receive") + ". If not provided, it will be asked for on startup.",
				Default:     "",
				Value:       serpent.StringOf(&authID),
			},
			{
				Flag:        "overlay-transport",
				Description: "The transport to use on the overlay. The overlay is used to exchange Wireguard nodes between peers. In DERP mode, nodes are exchanged over public Tailscale DERPs, while STUN mode sends nodes directly over UDP.",
				Default:     "derp",
				Value:       serpent.EnumOf(&overlayTransport, "derp", "stun"),
			},
			{
				Flag:    "stun-ip-override",
				Default: "",
				Value:   serpent.StringOf(&stunAddrOverride),
			},
			{
				Flag:        "stdio",
				Description: "Run SSH over stdin/stdout. This allows wush to be used as a transport for other programs, like rsync or regular ssh.",
				Default:     "false",
				Value:       serpent.BoolOf(&sshStdio),
			},
		},
	}
}

func waitUntilHasPeerHasIP(ctx context.Context, lc *tailscale.LocalClient) (netip.Addr, error) {
	for {
		select {
		case <-ctx.Done():
			return netip.Addr{}, ctx.Err()
		case <-time.After(time.Second):
		}

		stat, err := lc.Status(ctx)
		if err != nil {
			fmt.Println("error getting lc status:", err)
			continue
		}

		peers := stat.Peers()
		if len(peers) == 0 {
			// fmt.Println("No peer yet")
			continue
		}

		// fmt.Println("Received peer")

		peer, ok := stat.Peer[peers[0]]
		if !ok {
			fmt.Println("have peers but not found in map (developer error)")
			continue
		}

		if peer.Relay == "" {
			fmt.Println("peer no relay")
			continue
		}

		// fmt.Println("Peer active with relay", cliui.Code(peer.Relay))

		if len(peer.TailscaleIPs) == 0 {
			fmt.Println("peer has no ips (developer error)")
			continue
		}

		return peer.TailscaleIPs[0], nil
	}
}

func waitUntilHasP2P(ctx context.Context, lc *tailscale.LocalClient) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}

		stat, err := lc.Status(ctx)
		if err != nil {
			fmt.Println("error getting lc status:", err)
			continue
		}

		peers := stat.Peers()
		peer, ok := stat.Peer[peers[0]]
		if !ok {
			fmt.Println("no peer found in map while waiting p2p (developer error)")
			continue
		}

		if peer.Relay == "" {
			fmt.Println("peer no relay")
			continue
		}

		// fmt.Println("Peer active with relay", cliui.Code(peer.Relay))

		if len(peer.TailscaleIPs) == 0 {
			fmt.Println("peer has no ips (developer error)")
			continue
		}

		pingCancel, cancel := context.WithTimeout(ctx, time.Second)
		pong, err := lc.Ping(pingCancel, peer.TailscaleIPs[0], tailcfg.PingDisco)
		cancel()
		if err != nil {
			fmt.Println("ping failed:", err)
			continue
		}

		if pong.Endpoint == "" {
			fmt.Println("not p2p yet")
			continue
		}

		// fmt.Println("Peer active over p2p", cliui.Code(pong.Endpoint))
		return nil
	}
}
