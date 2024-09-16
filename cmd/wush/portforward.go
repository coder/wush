package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/xerrors"
	"tailscale.com/net/netns"
	"tailscale.com/tsnet"
	"tailscale.com/types/ptr"

	"github.com/coder/coder/v2/agent/agentssh"
	"github.com/coder/serpent"
	"github.com/coder/wush/cliui"
	"github.com/coder/wush/overlay"
	"github.com/coder/wush/tsserver"
)

func portForwardCmd() *serpent.Command {
	var (
		verbose bool
		logger  = new(slog.Logger)
		logf    = func(str string, args ...any) {}

		overlayOpts = new(sendOverlayOpts)
		send        = new(overlay.Send)
		tcpForwards []string // <port>:<port>
		udpForwards []string // <port>:<port>
	)
	return &serpent.Command{
		Use:   "port-forward",
		Short: "Transfer files.",
		Long: "Transfer files to a " + cliui.Code("wush") + " peer.\n" + formatExamples(
			example{
				Description: "Port forward a single TCP port from 1234 in the peer to port 5678 on your local machine",
				Command:     "wush port-forward --tcp 5678:1234",
			},
			example{
				Description: "Port forward a single UDP port from port 9000 to port 9000 on your local machine",
				Command:     "wush port-forward --udp 9000",
			},
			example{
				Description: "Port forward multiple TCP ports and a UDP port",
				Command:     "wush port-forward --tcp 8080:8080 --tcp 9000:3000 --udp 5353:53",
			},
			example{
				Description: "Port forward multiple ports (TCP or UDP) in condensed syntax",
				Command:     "wush port-forward --tcp 8080,9000:3000,9090-9092,10000-10002:10010-10012",
			},
			example{
				Description: "Port forward specifying the local address to bind to",
				Command:     "wush port-forward --tcp 1.2.3.4:8080:8080",
			},
		),
		Middleware: serpent.Chain(
			initLogger(&verbose, ptr.To(false), logger, &logf),
			initAuth(&overlayOpts.authKey, &overlayOpts.clientAuth),
			sendOverlayMW(overlayOpts, &send, logger, &logf),
		),
		Handler: func(inv *serpent.Invocation) error {
			ctx, cancel := context.WithCancel(inv.Context())
			defer cancel()

			specs, err := parsePortForwards(tcpForwards, udpForwards)
			if err != nil {
				return fmt.Errorf("parse port-forward specs: %w", err)
			}
			if len(specs) == 0 {
				return errors.New("no port-forwards requested")
			}

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

			var (
				wg                = new(sync.WaitGroup)
				listeners         = make([]net.Listener, len(specs))
				closeAllListeners = func() {
					logger.Debug("closing all listeners")
					for _, l := range listeners {
						if l == nil {
							continue
						}
						_ = l.Close()
					}
				}
			)
			defer closeAllListeners()

			for i, spec := range specs {
				l, err := listenAndPortForward(ctx, inv, ts, ip, wg, spec, logger)
				if err != nil {
					logger.Error("failed to listen", "spec", spec, "err", err)
					return err
				}
				listeners[i] = l
			}

			// Wait for the context to be canceled or for a signal and close
			// all listeners.
			var closeErr error
			wg.Add(1)
			go func() {
				defer wg.Done()

				sigs := make(chan os.Signal, 1)
				signal.Notify(sigs, os.Interrupt)

				select {
				case <-ctx.Done():
					logger.Debug("command context expired waiting for signal", "err", ctx.Err())
					closeErr = ctx.Err()
				case sig := <-sigs:
					logger.Debug("received signal", "signal", sig)
					_, _ = fmt.Fprintln(inv.Stderr, "\nReceived signal, closing all listeners and active connections")
				}

				cancel()
				closeAllListeners()
			}()

			wg.Wait()
			return closeErr
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
			{
				Flag:          "tcp",
				FlagShorthand: "p",
				Env:           "WUSH_PORT_FORWARD_TCP",
				Description:   "Forward TCP port(s) from the peer to the local machine.",
				Value:         serpent.StringArrayOf(&tcpForwards),
			},
			{
				Flag:        "udp",
				Env:         "WUSH_PORT_FORWARD_UDP",
				Description: "Forward UDP port(s) from the peer to the local machine. The UDP connection has TCP-like semantics to support stateful UDP protocols.",
				Value:       serpent.StringArrayOf(&udpForwards),
			},
		},
	}
}

func listenAndPortForward(
	ctx context.Context,
	inv *serpent.Invocation,
	ts *tsnet.Server,
	remoteIP netip.Addr,
	wg *sync.WaitGroup,
	spec portForwardSpec,
	logger *slog.Logger,
) (net.Listener, error) {
	logger = logger.With("network", spec.listenNetwork, "address", spec.listenAddress)
	_, _ = fmt.Fprintf(inv.Stderr, "Forwarding '%v://%v' locally to '%v://%v' in the peer\n", spec.listenNetwork, spec.listenAddress, spec.dialNetwork, spec.dialAddress)

	l, err := inv.Net.Listen(spec.listenNetwork, spec.listenAddress.String())
	if err != nil {
		return nil, xerrors.Errorf("listen '%v://%v': %w", spec.listenNetwork, spec.listenAddress, err)
	}
	logger.Debug("listening")

	wg.Add(1)
	go func(spec portForwardSpec) {
		defer wg.Done()
		for {
			netConn, err := l.Accept()
			if err != nil {
				// Silently ignore net.ErrClosed errors.
				if errors.Is(err, net.ErrClosed) {
					logger.Debug("listener closed")
					return
				}
				_, _ = fmt.Fprintf(inv.Stderr, "Error accepting connection from '%v://%v': %v\n", spec.listenNetwork, spec.listenAddress, err)
				_, _ = fmt.Fprintln(inv.Stderr, "Killing listener")
				return
			}
			logger.Debug("accepted connection", "remote_addr", netConn.RemoteAddr())

			go func(netConn net.Conn) {
				defer netConn.Close()
				addr := netip.AddrPortFrom(remoteIP, spec.dialAddress.Port())
				remoteConn, err := ts.Dial(ctx, spec.dialNetwork, addr.String())
				if err != nil {
					_, _ = fmt.Fprintf(inv.Stderr, "Failed to dial '%v://%v' in peer: %s\n", spec.dialNetwork, addr, err)
					return
				}
				defer remoteConn.Close()
				logger.Debug("dialed remote", "remote_addr", netConn.RemoteAddr())

				agentssh.Bicopy(ctx, netConn, remoteConn)
				logger.Debug("connection closing", "remote_addr", netConn.RemoteAddr())
			}(netConn)
		}
	}(spec)

	return l, nil
}

type portForwardSpec struct {
	listenNetwork string // tcp, udp
	listenAddress netip.AddrPort

	dialNetwork string // tcp, udp
	dialAddress netip.AddrPort
}

func parsePortForwards(tcpSpecs, udpSpecs []string) ([]portForwardSpec, error) {
	specs := []portForwardSpec{}

	for _, specEntry := range tcpSpecs {
		for _, spec := range strings.Split(specEntry, ",") {
			ports, err := parseSrcDestPorts(spec)
			if err != nil {
				return nil, xerrors.Errorf("failed to parse TCP port-forward specification %q: %w", spec, err)
			}

			for _, port := range ports {
				specs = append(specs, portForwardSpec{
					listenNetwork: "tcp",
					listenAddress: port.local,
					dialNetwork:   "tcp",
					dialAddress:   port.remote,
				})
			}
		}
	}

	for _, specEntry := range udpSpecs {
		for _, spec := range strings.Split(specEntry, ",") {
			ports, err := parseSrcDestPorts(spec)
			if err != nil {
				return nil, xerrors.Errorf("failed to parse UDP port-forward specification %q: %w", spec, err)
			}

			for _, port := range ports {
				specs = append(specs, portForwardSpec{
					listenNetwork: "udp",
					listenAddress: port.local,
					dialNetwork:   "udp",
					dialAddress:   port.remote,
				})
			}
		}
	}

	// Check for duplicate entries.
	locals := map[string]struct{}{}
	for _, spec := range specs {
		localStr := fmt.Sprintf("%v:%v", spec.listenNetwork, spec.listenAddress)
		if _, ok := locals[localStr]; ok {
			return nil, xerrors.Errorf("local %v %v is specified twice", spec.listenNetwork, spec.listenAddress)
		}
		locals[localStr] = struct{}{}
	}

	return specs, nil
}

func parsePort(in string) (uint16, error) {
	port, err := strconv.ParseUint(strings.TrimSpace(in), 10, 16)
	if err != nil {
		return 0, xerrors.Errorf("parse port %q: %w", in, err)
	}
	if port == 0 {
		return 0, xerrors.New("port cannot be 0")
	}

	return uint16(port), nil
}

type parsedSrcDestPort struct {
	local, remote netip.AddrPort
}

func parseSrcDestPorts(in string) ([]parsedSrcDestPort, error) {
	var (
		err        error
		parts      = strings.Split(in, ":")
		localAddr  = netip.AddrFrom4([4]byte{127, 0, 0, 1})
		remoteAddr = netip.AddrFrom4([4]byte{127, 0, 0, 1})
	)

	switch len(parts) {
	case 1:
		// Duplicate the single part
		parts = append(parts, parts[0])
	case 2:
		// Check to see if the first part is an IP address.
		_localAddr, err := netip.ParseAddr(parts[0])
		if err != nil {
			break
		}
		// The first part is the local address, so duplicate the port.
		localAddr = _localAddr
		parts = []string{parts[1], parts[1]}

	case 3:
		_localAddr, err := netip.ParseAddr(parts[0])
		if err != nil {
			return nil, xerrors.Errorf("invalid port specification %q; invalid ip %q: %w", in, parts[0], err)
		}
		localAddr = _localAddr
		parts = parts[1:]

	default:
		return nil, xerrors.Errorf("invalid port specification %q", in)
	}

	if !strings.Contains(parts[0], "-") {
		localPort, err := parsePort(parts[0])
		if err != nil {
			return nil, xerrors.Errorf("parse local port from %q: %w", in, err)
		}
		remotePort, err := parsePort(parts[1])
		if err != nil {
			return nil, xerrors.Errorf("parse remote port from %q: %w", in, err)
		}

		return []parsedSrcDestPort{{
			local:  netip.AddrPortFrom(localAddr, localPort),
			remote: netip.AddrPortFrom(remoteAddr, remotePort),
		}}, nil
	}

	local, err := parsePortRange(parts[0])
	if err != nil {
		return nil, xerrors.Errorf("parse local port range from %q: %w", in, err)
	}
	remote, err := parsePortRange(parts[1])
	if err != nil {
		return nil, xerrors.Errorf("parse remote port range from %q: %w", in, err)
	}
	if len(local) != len(remote) {
		return nil, xerrors.Errorf("port ranges must be the same length, got %d ports forwarded to %d ports", len(local), len(remote))
	}
	var out []parsedSrcDestPort
	for i := range local {
		out = append(out, parsedSrcDestPort{
			local:  netip.AddrPortFrom(localAddr, local[i]),
			remote: netip.AddrPortFrom(remoteAddr, remote[i]),
		})
	}
	return out, nil
}

func parsePortRange(in string) ([]uint16, error) {
	parts := strings.Split(in, "-")
	if len(parts) != 2 {
		return nil, xerrors.Errorf("invalid port range specification %q", in)
	}
	start, err := parsePort(parts[0])
	if err != nil {
		return nil, xerrors.Errorf("parse range start port from %q: %w", in, err)
	}
	end, err := parsePort(parts[1])
	if err != nil {
		return nil, xerrors.Errorf("parse range end port from %q: %w", in, err)
	}
	if end < start {
		return nil, xerrors.Errorf("range end port %v is less than start port %v", end, start)
	}
	var ports []uint16
	for i := start; i <= end; i++ {
		ports = append(ports, i)
	}
	return ports, nil
}
