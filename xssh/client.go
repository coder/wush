package xssh

import (
	"context"
	"os"
	"strings"

	"github.com/coder/coder/v2/pty"
	"github.com/coder/serpent"
	"github.com/mattn/go-isatty"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	"golang.org/x/xerrors"
	"tailscale.com/tsnet"
)

func TailnetSSH(ctx context.Context, inv *serpent.Invocation, ts *tsnet.Server, addr string, stdio bool) error {
	conn, err := ts.Dial(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	// if stdio {
	// 	gnConn, ok := conn.(*gonet.TCPConn)
	// 	if !ok {
	// 		panic("ssh tcp conn is not *gonet.TCPConn")
	// 	}
	// }

	sshConn, channels, requests, err := ssh.NewClientConn(conn, "localhost:22", &ssh.ClientConfig{
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		return err
	}

	sshClient := ssh.NewClient(sshConn, channels, requests)
	sshSession, err := sshClient.NewSession()
	if err != nil {
		return err
	}

	sshSession.Stdin = inv.Stdin
	sshSession.Stdout = inv.Stdout
	sshSession.Stderr = inv.Stderr

	if len(inv.Args) > 1 {
		return sshSession.Run(strings.Join(inv.Args, " "))
	}

	stdinFile, validIn := inv.Stdin.(*os.File)
	stdoutFile, validOut := inv.Stdout.(*os.File)
	if validIn && validOut && isatty.IsTerminal(stdinFile.Fd()) && isatty.IsTerminal(stdoutFile.Fd()) {
		inState, err := pty.MakeInputRaw(stdinFile.Fd())
		if err != nil {
			return err
		}
		defer func() {
			_ = pty.RestoreTerminal(stdinFile.Fd(), inState)
		}()
		outState, err := pty.MakeOutputRaw(stdoutFile.Fd())
		if err != nil {
			return err
		}
		defer func() {
			_ = pty.RestoreTerminal(stdoutFile.Fd(), outState)
		}()

		windowChange := ListenWindowSize(ctx)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-windowChange:
				}
				width, height, err := term.GetSize(int(stdoutFile.Fd()))
				if err != nil {
					continue
				}
				_ = sshSession.WindowChange(height, width)
			}
		}()
	}

	err = sshSession.RequestPty("xterm-256color", 128, 128, ssh.TerminalModes{})
	if err != nil {
		return xerrors.Errorf("request pty: %w", err)
	}

	err = sshSession.Shell()
	if err != nil {
		return xerrors.Errorf("start shell: %w", err)
	}

	if validOut {
		// Set initial window size.
		width, height, err := term.GetSize(int(stdoutFile.Fd()))
		if err == nil {
			_ = sshSession.WindowChange(height, width)
		}
	}

	return sshSession.Wait()
}
