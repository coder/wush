package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/coder/serpent"
	"github.com/coder/wush/cliui"
	"github.com/coder/wush/overlay"
	"github.com/coder/wush/tsserver"
	"github.com/creack/pty"
	"github.com/mattn/go-isatty"
	"golang.org/x/crypto/ssh/terminal"
	"tailscale.com/net/netns"
)

func shareCmd() *serpent.Command {
	var (
		overlayType string
		verbose     bool
		enabled     = []string{}
		disabled    = []string{}
	)
	return &serpent.Command{
		Use:     "share",
		Aliases: []string{},
		Short:   "Share a terminal.",
		Long:    "Share a terminal.",
		Handler: func(inv *serpent.Invocation) error {
			ctx := inv.Context()
			defer fmt.Println("[exited]")

			// Switch to the alternate screen buffer
			fmt.Print("\033[?1049h")
			// Reset cursor to the top-left corner
			fmt.Print("\033[H")
			// Switch back to the main screen buffer on exit
			defer fmt.Print("\033[?1049l")

			var logSink io.Writer = io.Discard
			if verbose {
				logSink = inv.Stderr
			}
			logger := slog.New(slog.NewTextHandler(logSink, nil))
			hlog := func(format string, args ...any) {
				fmt.Fprintf(inv.Stderr, format+"\n", args...)
			}
			dm, err := tsserver.DERPMapTailscale(ctx)
			if err != nil {
				return err
			}
			// r := overlay.NewReceiveOverlay(logger, hlog, dm)
			r := overlay.NewReceiveOverlay(logger, func(format string, args ...any) {}, dm)

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

			// Ensure we always print the auth key on stdout
			if isatty.IsTerminal(os.Stdout.Fd()) {
				hlog("Your auth key is:")
				fmt.Println("  |", cliui.Code(r.ClientAuth().AuthKey()))
				fmt.Println("  |", cliui.Code("http://localhost:5173/connect#"+r.ClientAuth().AuthKey()))
				hlog("Use this key to authenticate other " + cliui.Code("wush") + " commands to this instance.")
			} else {
				fmt.Println(cliui.Code(r.ClientAuth().AuthKey()))
				hlog("The auth key has been printed to stdout")
			}

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

			hlog("WireGuard is ready")

			ll, err := ts.Listen("tcp", ":33")
			if err != nil {
				return err
			}

			shell := os.Getenv("SHELL")
			if shell == "" {
				shell = "/bin/sh"
			}

			// Save the current state of the terminal
			oldState, err := terminal.MakeRaw(int(os.Stdin.Fd()))
			if err != nil {
				panic(err)
			}
			defer func() {
				_ = terminal.Restore(int(os.Stdin.Fd()), oldState)
			}()

			cmd := exec.Command(shell)
			ptmx, err := pty.Start(cmd)
			if err != nil {
				log.Fatal(err)
			}
			defer func() { _ = ptmx.Close() }()

			// Handle pty size.
			ch := make(chan os.Signal, 1)
			signal.Notify(ch, syscall.SIGWINCH)
			go func() {
				for range ch {
					if err := pty.InheritSize(os.Stdin, ptmx); err != nil {
						log.Printf("error resizing pty: %s", err)
					}
				}
			}()
			ch <- syscall.SIGWINCH                        // Initial resize.
			defer func() { signal.Stop(ch); close(ch) }() // Cleanup signals when done.

			// Copy stdin to the pty and the pty to stdout.
			// NOTE: The goroutine will keep reading until the next keystroke before returning.
			go func() { _, _ = io.Copy(ptmx, os.Stdin) }()
			mw := &multiWriter{wrs: map[int64]io.Writer{}}
			buf := bytes.NewBuffer(nil)
			mw.AddWriter(buf)
			mw.AddWriter(unclose{os.Stdout})
			go func() { _, _ = io.Copy(mw, ptmx) }()

			go func() {
				cmd.Wait()
				mw.Close()
				ll.Close()
			}()

			for {
				conn, err := ll.Accept()
				if err != nil {
					return nil
				}

				mw.lock()
				_, _ = io.Copy(conn, buf)
				mw.unlock()

				close := mw.AddWriter(conn)
				go func() { defer close(); _, _ = io.Copy(ptmx, conn) }()
			}

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
			{
				Flag:        "enable",
				Description: "Server options to enable.",
				Default:     "ssh,cp,port-forward",
				Value:       serpent.EnumArrayOf(&enabled, "ssh", "cp", "port-forward"),
			},
			{
				Flag:        "disable",
				Description: "Server options to disable.",
				Default:     "",
				Value:       serpent.EnumArrayOf(&disabled, "ssh", "cp", "port-forward"),
			},
		},
	}
}

type multiWriter struct {
	mu     sync.Mutex
	wrs    map[int64]io.Writer
	closed bool
}

func (mw *multiWriter) lock() {
	mw.mu.Lock()
}

func (mw *multiWriter) unlock() {
	mw.mu.Unlock()
}

func (mw *multiWriter) Close() error {
	mw.mu.Lock()
	defer mw.mu.Unlock()
	if mw.closed {
		return nil
	}
	mw.closed = true
	for _, w := range mw.wrs {
		if closer, ok := w.(io.Closer); ok {
			_ = closer.Close()
		}
	}
	return nil
}

func (mw *multiWriter) Write(p []byte) (int, error) {
	mw.mu.Lock()
	defer mw.mu.Unlock()
	if mw.closed {
		return 0, fs.ErrClosed
	}
	// var total int
	for _, w := range mw.wrs {
		n, err := w.Write(p)
		if err != nil {
			continue
		}
		_ = n
	}
	return len(p), nil
}

func (mw *multiWriter) AddWriter(w io.Writer) func() {
	mw.mu.Lock()
	defer mw.mu.Unlock()
	id := time.Now().UnixNano()
	mw.wrs[id] = w
	return func() {
		mw.mu.Lock()
		defer mw.mu.Unlock()
		delete(mw.wrs, id)
	}
}

type unclose struct {
	w io.Writer
}

func (u unclose) Write(p []byte) (int, error) {
	return u.w.Write(p)
}
