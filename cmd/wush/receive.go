package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/afero"
	"golang.org/x/xerrors"
	"tailscale.com/ipn/store"
	"tailscale.com/net/netns"
	"tailscale.com/tsnet"

	cslog "cdr.dev/slog"
	csloghuman "cdr.dev/slog/sloggers/sloghuman"
	"github.com/coder/coder/v2/agent/agentssh"
	"github.com/coder/serpent"
	"github.com/coder/wush/cliui"
	"github.com/coder/wush/overlay"
	"github.com/coder/wush/tsserver"
)

func receiveCmd() *serpent.Command {
	var (
		overlayType string
		verbose     bool
	)
	return &serpent.Command{
		Use:     "receive",
		Aliases: []string{"host"},
		Long:    "Runs the wush server. Allows other wush CLIs to connect to this computer.",
		Handler: func(inv *serpent.Invocation) error {
			ctx := inv.Context()
			var logSink io.Writer = io.Discard
			if verbose {
				logSink = inv.Stderr
			}
			logger := slog.New(slog.NewTextHandler(logSink, nil))
			dm, err := tsserver.DERPMapTailscale(ctx)
			if err != nil {
				return err
			}
			r := overlay.NewReceiveOverlay(logger, dm)

			switch overlayType {
			case "derp":
				err = r.PickDERPHome(ctx)
				if err != nil {
					return err
				}
				go r.ListenOverlayDERP(ctx)

			case "stun":
				waitStun, err := r.ListenOverlaySTUN(ctx)
				if err != nil {
					return fmt.Errorf("get stun addr: %w", err)
				}
				<-waitStun

			default:
				return fmt.Errorf("unknown overlay type: %s", overlayType)
			}

			fmt.Println("Your auth key is:")
			fmt.Println("\t>", cliui.Code(r.ClientAuth().AuthKey()))
			fmt.Println("Use this key to authenticate other", cliui.Code("wush"), "commands to this instance.")

			s, err := tsserver.NewServer(ctx, logger, r)
			if err != nil {
				return err
			}

			go s.ListenAndServe(ctx)
			netns.SetDialerOverride(s.Dialer())
			ts, err := newTSNet("receive")
			if err != nil {
				return err
			}

			ts.Up(ctx)
			fs := afero.NewOsFs()

			fmt.Println(cliui.Timestamp(time.Now()), "Wireguard is ready")

			sshSrv, err := agentssh.NewServer(ctx,
				cslog.Make(csloghuman.Sink(logSink)),
				prometheus.NewRegistry(),
				fs,
				nil,
			)
			if err != nil {
				return err
			}

			ls, err := ts.Listen("tcp", ":3")
			if err != nil {
				return err
			}

			go func() {
				fmt.Println(cliui.Timestamp(time.Now()), "SSH server listening")
				err := sshSrv.Serve(ls)
				if err != nil {
					logger.Info("ssh server exited", "err", err)
				}
			}()

			ctx, ctxCancel := inv.SignalNotifyContext(ctx, os.Interrupt)
			defer ctxCancel()

			<-ctx.Done()
			return sshSrv.Close()
		},
		Options: []serpent.Option{
			{
				Flag:    "overlay-type",
				Default: "derp",
				Value:   serpent.EnumOf(&overlayType, "derp", "stun"),
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

func newTSNet(direction string) (*tsnet.Server, error) {
	var err error
	tmp := os.TempDir()
	srv := new(tsnet.Server)
	srv.Dir = tmp
	srv.Hostname = "wush-" + direction
	srv.Ephemeral = true
	srv.AuthKey = direction
	srv.ControlURL = "http://localhost:8080"
	srv.Logf = func(format string, args ...any) {}
	// srv.Logf = func(format string, args ...any) {
	// 	fmt.Printf(format+"\n", args...)
	// }
	srv.UserLogf = func(format string, args ...any) {}
	// srv.UserLogf = func(format string, args ...any) {
	// 	fmt.Printf(format+"\n", args...)
	// }

	srv.Store, err = store.New(func(format string, args ...any) {}, "mem:wush")
	if err != nil {
		return nil, xerrors.Errorf("create state store: %w", err)
	}

	return srv, nil
}
