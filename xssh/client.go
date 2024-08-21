package xssh

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/coder/v2/pty"
	"github.com/coder/serpent"
	"github.com/mattn/go-isatty"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	"golang.org/x/xerrors"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
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

	if len(inv.Args) > 0 {
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

type rawSSHCopier struct {
	conn   *gonet.TCPConn
	logger *slog.Logger
	r      io.Reader
	w      io.Writer

	done chan struct{}
}

func newRawSSHCopier(logger *slog.Logger, conn *gonet.TCPConn, r io.Reader, w io.Writer) *rawSSHCopier {
	return &rawSSHCopier{conn: conn, logger: logger, r: r, w: w, done: make(chan struct{})}
}

func (c *rawSSHCopier) copy(wg *sync.WaitGroup) {
	defer close(c.done)
	logCtx := context.Background()
	wg.Add(1)
	go func() {
		defer wg.Done()
		// We close connections using CloseWrite instead of Close, so that the SSH server sees the
		// closed connection while reading, and shuts down cleanly.  This will trigger the io.Copy
		// in the server-to-client direction to also be closed and the copy() routine will exit.
		// This ensures that we don't leave any state in the server, like forwarded ports if
		// copy() were to return and the underlying tailnet connection torn down before the TCP
		// session exits. This is a bit of a hack to block shut down at the application layer, since
		// we can't serialize the TCP and tailnet layers shutting down.
		//
		// Of course, if the underlying transport is broken, io.Copy will still return.
		defer func() {
			cwErr := c.conn.CloseWrite()
			c.logger.DebugContext(logCtx, "closed raw SSH connection for writing", "err", cwErr)
		}()

		_, err := io.Copy(c.conn, c.r)
		if err != nil {
			c.logger.ErrorContext(logCtx, "copy stdin error", "err", err)
		} else {
			c.logger.DebugContext(logCtx, "copy stdin complete")
		}
	}()
	_, err := io.Copy(c.w, c.conn)
	if err != nil {
		c.logger.ErrorContext(logCtx, "copy stdout error", "err", err)
	} else {
		c.logger.DebugContext(logCtx, "copy stdout complete")
	}
}

func (c *rawSSHCopier) Close() error {
	err := c.conn.CloseWrite()

	// give the copy() call a chance to return on a timeout, so that we don't
	// continue tearing down and close the underlying netstack before the SSH
	// session has a chance to gracefully shut down.
	t := time.NewTimer(5 * time.Second)
	defer t.Stop()
	select {
	case <-c.done:
	case <-t.C:
	}
	return err
}
