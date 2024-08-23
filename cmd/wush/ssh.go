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
		authKey            string
		waitP2P            bool
		stunAddrOverride   string
		stunAddrOverrideIP netip.Addr
		sshStdio           bool
	)
	return &serpent.Command{
		Use:     "wush",
		Aliases: []string{"ssh"},
		Long: "Opens an SSH connection to a " + cliui.Code("wush") + " peer. " +
			"Use " + cliui.Code("wush receive") + " on the computer you would like to connect to.",
		Handler: func(inv *serpent.Invocation) error {

			ctx := inv.Context()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			logF := func(str string, args ...any) {
				if sshStdio {
					return
				}
				fmt.Fprintf(inv.Stderr, str+"\n", args...)
			}
			if authKey == "" {
				err := huh.NewInput().
					Title("Enter the receiver's Auth key:").
					Value(&authKey).
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

			err = send.Auth.Parse(authKey)
			if err != nil {
				return fmt.Errorf("parse auth key: %w", err)
			}

			logF("Auth information:")
			stunStr := send.Auth.ReceiverStunAddr.String()
			if !send.Auth.ReceiverStunAddr.IsValid() {
				stunStr = "Disabled"
			}
			logF("\t> Server overlay STUN address: %s", cliui.Code(stunStr))
			derpStr := "Disabled"
			if send.Auth.ReceiverDERPRegionID > 0 {
				derpStr = dm.Regions[int(send.Auth.ReceiverDERPRegionID)].RegionName
			}
			logF("\t> Server overlay DERP home:    %s", cliui.Code(derpStr))
			logF("\t> Server overlay public key:   %s", cliui.Code(send.Auth.ReceiverPublicKey.ShortString()))
			logF("\t> Server overlay auth key:     %s", cliui.Code(send.Auth.OverlayPrivateKey.Public().ShortString()))

			s, err := tsserver.NewServer(ctx, logger, send)
			if err != nil {
				return err
			}

			if send.Auth.ReceiverDERPRegionID != 0 {
				go send.ListenOverlayDERP(ctx)
			} else if send.Auth.ReceiverStunAddr.IsValid() {
				go send.ListenOverlaySTUN(ctx)
			} else {
				return errors.New("auth key provided neither DERP nor STUN")
			}

			go s.ListenAndServe(ctx)
			netns.SetDialerOverride(s.Dialer())
			ts, err := newTSNet("send")
			if err != nil {
				return err
			}
			ts.Logf = func(string, ...any) {}
			ts.UserLogf = func(string, ...any) {}

			logF("Bringing Wireguard up..")
			ts.Up(ctx)
			logF("Wireguard is ready!")

			lc, err := ts.LocalClient()
			if err != nil {
				return err
			}

			ip, err := waitUntilHasPeerHasIP(ctx, logF, lc)
			if err != nil {
				return err
			}

			if waitP2P {
				err := waitUntilHasP2P(ctx, logF, lc)
				if err != nil {
					return err
				}
			}

			return xssh.TailnetSSH(ctx, inv, ts, ip.String()+":3", sshStdio)
		},
		Options: []serpent.Option{
			{
				Flag:        "auth",
				Env:         "WUSH_AUTH",
				Description: "The auth key returned by " + cliui.Code("wush receive") + ". If not provided, it will be asked for on startup.",
				Default:     "",
				Value:       serpent.StringOf(&authKey),
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
			{
				Flag:        "wait-p2p",
				Description: "Waits for the connection to be p2p.",
				Default:     "false",
				Value:       serpent.BoolOf(&sshStdio),
			},
		},
	}
}

func waitUntilHasPeerHasIP(ctx context.Context, logF func(str string, args ...any), lc *tailscale.LocalClient) (netip.Addr, error) {
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
			logF("No peer yet")
			continue
		}

		logF("Received peer")

		peer, ok := stat.Peer[peers[0]]
		if !ok {
			logF("have peers but not found in map (developer error)")
			continue
		}

		if peer.Relay == "" {
			logF("peer no relay")
			continue
		}

		logF("Peer active with relay %s", cliui.Code(peer.Relay))

		if len(peer.TailscaleIPs) == 0 {
			logF("peer has no ips (developer error)")
			continue
		}

		return peer.TailscaleIPs[0], nil
	}
}

func waitUntilHasP2P(ctx context.Context, logF func(str string, args ...any), lc *tailscale.LocalClient) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}

		stat, err := lc.Status(ctx)
		if err != nil {
			logF("error getting lc status: %s", err)
			continue
		}

		peers := stat.Peers()
		peer, ok := stat.Peer[peers[0]]
		if !ok {
			logF("no peer found in map while waiting p2p (developer error)")
			continue
		}

		if peer.Relay == "" {
			logF("peer no relay")
			continue
		}

		if len(peer.TailscaleIPs) == 0 {
			logF("peer has no ips (developer error)")
			continue
		}

		pingCancel, cancel := context.WithTimeout(ctx, time.Second)
		pong, err := lc.Ping(pingCancel, peer.TailscaleIPs[0], tailcfg.PingDisco)
		cancel()
		if err != nil {
			logF("ping failed: %s", err)
			continue
		}

		if pong.Endpoint == "" {
			logF("Not p2p yet")
			continue
		}

		logF("Peer active over p2p %s", cliui.Code(pong.Endpoint))
		return nil
	}
}
