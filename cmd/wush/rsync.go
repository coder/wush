package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os/exec"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/coder/serpent"
	"github.com/coder/wush/cliui"
	"github.com/coder/wush/overlay"
	"github.com/coder/wush/tsserver"
)

func rsyncCmd() *serpent.Command {
	var (
		authID             string
		overlayTransport   string
		stunAddrOverride   string
		stunAddrOverrideIP netip.Addr
		sshStdio           bool
	)
	return &serpent.Command{
		Use: "rsync",
		Long: "Runs rsync to transfer files to a " + cliui.Code("wush") + " peer. " +
			"Use " + cliui.Code("wush receive") + " on the computer you would like to connect to.",
		Handler: func(inv *serpent.Invocation) error {
			ctx := inv.Context()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))

			if authID == "" {
				err := huh.NewInput().
					Title("Enter your Auth ID:").
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

			args := []string{
				"-c",
				"rsync --progress --stats -avz --human-readable " + fmt.Sprintf("-e=\"wush --auth-id %s --stdio --\" ", send.Auth.AuthKey()) + strings.Join(inv.Args, " "),
			}
			fmt.Println("Running: rsync", args)
			cmd := exec.CommandContext(ctx, "sh", args...)
			cmd.Stdin = inv.Stdin
			cmd.Stdout = inv.Stdout
			cmd.Stderr = inv.Stderr

			return cmd.Run()
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
