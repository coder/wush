package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"tailscale.com/types/ptr"

	"github.com/coder/serpent"
	"github.com/coder/wush/cliui"
	"github.com/coder/wush/tsserver"
)

func rsyncCmd() *serpent.Command {
	var (
		verbose bool
		logger  = new(slog.Logger)
		logf    = func(str string, args ...any) {}

		overlayOpts = new(sendOverlayOpts)
	)
	return &serpent.Command{
		Use:   "rsync [flags] -- [rsync args]",
		Short: "Transfer files over rsync.",
		Long: "Runs rsync to transfer files to a " + cliui.Code("wush") + " peer. " +
			"Use " + cliui.Code("wush serve") + " on the computer you would like to connect to." +
			"\n\n" +
			formatExamples(
				example{
					Description: "Sync a local file to the remote",
					Command:     "wush rsync /local/path :/remote/path",
				},
				example{
					Description: "Download a remote file to the local computer",
					Command:     "wush rsync :/remote/path /local/path",
				},
				example{
					Description: "Add rsync flags",
					Command:     "wush rsync /local/path :/remote/path -- --progress --stats -avz --human-readable",
				},
			),
		Middleware: serpent.Chain(
			initLogger(&verbose, ptr.To(false), logger, &logf),
			initAuth(&overlayOpts.authKey, &overlayOpts.clientAuth),
		),
		Handler: func(inv *serpent.Invocation) error {
			ctx := inv.Context()

			dm, err := tsserver.DERPMapTailscale(inv.Context())
			if err != nil {
				return err
			}
			overlayOpts.clientAuth.PrintDebug(logf, dm)

			progPath := os.Args[0]
			args := []string{
				"-c",
				fmt.Sprintf(`rsync -e "%s ssh --auth-key %s --quiet --" %s`,
					progPath, overlayOpts.clientAuth.AuthKey(), strings.Join(inv.Args, " "),
				),
			}
			fmt.Println(args)
			fmt.Println("Running: rsync", strings.Join(inv.Args, " "))
			cmd := exec.CommandContext(ctx, "sh", args...)
			cmd.Stdin = inv.Stdin
			cmd.Stdout = inv.Stdout
			cmd.Stderr = inv.Stderr

			return cmd.Run()
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
				Flag:    "stun-ip-override",
				Default: "",
				Value:   serpent.StringOf(&overlayOpts.stunAddrOverride),
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
