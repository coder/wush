package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

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
		verbose   bool
		quiet     bool
		derpmapFi string
		logger    = new(slog.Logger)
		logf      = func(str string, args ...any) {}

		dm          = new(tailcfg.DERPMap)
		overlayOpts = new(sendOverlayOpts)
		send        = new(overlay.Send)
	)
	return &serpent.Command{
		Use:     "ssh",
		Aliases: []string{},
		Short:   "Open a SSH connection to a wush server.",
		Long:    "Use " + cliui.Code("wush serve") + " on the computer you would like to connect to.",
		Middleware: serpent.Chain(
			initLogger(&verbose, &quiet, logger, &logf),
			initAuth(&overlayOpts.authKey, &overlayOpts.clientAuth),
			derpMap(&derpmapFi, dm),
			sendOverlayMW(overlayOpts, &send, logger, dm, &logf),
		),
		Handler: func(inv *serpent.Invocation) error {
			ctx := inv.Context()

			s, err := tsserver.NewServer(ctx, logger, send, dm)
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
			ts, err := newTSNet("send", verbose)
			if err != nil {
				return err
			}

			logf("Bringing WireGuard up..")
			ts.Up(ctx)
			logf("WireGuard is ready!")

			lc, err := ts.LocalClient()
			if err != nil {
				return err
			}

			ip, err := waitUntilHasPeerHasIP(ctx, logf, lc)
			if err != nil {
				return err
			}

			if overlayOpts.waitP2P {
				err := waitUntilHasP2P(ctx, logf, lc)
				if err != nil {
					return err
				}
			}

			return xssh.TailnetSSH(ctx, inv, ts, netip.AddrPortFrom(ip, 3).String(), quiet)
		},
		Options: []serpent.Option{
			{
				Flag:        "auth-key",
				Env:         "WUSH_AUTH_KEY",
				Description: "The auth key returned by " + cliui.Code("wush serve") + ". If not provided, it will be asked for on startup.",
				Default:     "",
				Value:       serpent.StringOf(&overlayOpts.authKey),
			},
			{
				Flag:        "derp-config-file",
				Description: "File which specifies the DERP config to use. In the structure of https://pkg.go.dev/tailscale.com@v1.74.1/tailcfg#DERPMap.",
				Default:     "",
				Value:       serpent.StringOf(&derpmapFi),
			},
			{
				Flag:    "stun-ip-override",
				Default: "",
				Value:   serpent.StringOf(&overlayOpts.stunAddrOverride),
			},
			{
				Flag:        "quiet",
				Description: "Silences all output.",
				Default:     "false",
				Value:       serpent.BoolOf(&quiet),
			},
			{
				Flag:        "wait-p2p",
				Description: "Waits for the connection to be p2p.",
				Default:     "false",
				Value:       serpent.BoolOf(&overlayOpts.waitP2P),
			},
			{
				Flag:          "verbose",
				FlagShorthand: "v",
				Description:   "Enable verbose logging.",
				Default:       "false",
				Value:         serpent.BoolOf(&verbose),
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
