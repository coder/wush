// Package tsserver implements the Tailscale coordination protocol for a single
// client. Heavy inspiration was taken from https://github.com/juanfont/headscale
package tsserver

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/wush/overlay"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	"github.com/puzpuzpuz/xsync/v3"
	"github.com/valyala/fasthttp/fasthttputil"
	xslices "golang.org/x/exp/slices"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"tailscale.com/control/controlbase"
	"tailscale.com/control/controlhttp"
	"tailscale.com/net/netns"
	"tailscale.com/smallzstd"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/opt"
	"tailscale.com/types/ptr"
)

func DERPMapTailscale(ctx context.Context) (*tailcfg.DERPMap, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://controlplane.tailscale.com/derpmap/default", nil)
	if err != nil {
		return nil, fmt.Errorf("make ts derpmap req: %w", err)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get ts derpmap: %w", err)
	}
	defer res.Body.Close()

	dm := &tailcfg.DERPMap{}
	err = json.NewDecoder(res.Body).Decode(dm)
	if err != nil {
		return nil, fmt.Errorf("decode ts derpmap: %w", err)
	}

	return dm, nil
}

func NewServer(ctx context.Context, logger *slog.Logger, ov overlay.Overlay) (*server, error) {
	dm, err := DERPMapTailscale(ctx)
	if err != nil {
		return nil, err
	}

	s := &server{
		logger:          logger,
		noisePrivateKey: key.NewMachine(),
		nodeUpdate:      make(chan struct{}, 8),
		ll:              fasthttputil.NewInmemoryListener(),
		ml:              &memListen{listen: make(chan net.Conn)},
		overlay:         ov,
		derpMap:         dm,

		peerMap:       xsync.NewMapOf[key.NodePublic, *tailcfg.Node](),
		peerMapUpdate: make(chan update, 8),
	}

	return s, nil
}

type server struct {
	logger          *slog.Logger
	derpMap         *tailcfg.DERPMap
	noisePrivateKey key.MachinePrivate
	ll              *fasthttputil.InmemoryListener
	ml              *memListen

	overlay overlay.Overlay

	node       atomic.Pointer[tailcfg.Node]
	nodeUpdate chan struct{}

	peerMap       *xsync.MapOf[key.NodePublic, *tailcfg.Node]
	peerMapUpdate chan update
}

func (s *server) ListenAndServe(_ context.Context) error {
	r := chi.NewRouter()
	r.NotFound(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.logger.Info("main handler not found", "path", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))

	r.Get("/key", s.KeyHandler)
	r.Post("/ts2021", s.NoiseUpgradeHandler)

	go func() {
		for {
			select {
			case node := <-s.overlay.Recv():
				s.peerMap.Store(node.Key, node)
				s.peerMapUpdate <- update{
					ty:   updateTypeNewPeer,
					node: node,
				}
			case <-s.nodeUpdate:
				s.overlay.Send() <- s.node.Load()
			}
		}
	}()

	return http.Serve(s.ml, r)
}

type memListen struct {
	listen chan net.Conn
}

// Accept waits for and returns the next connection to the listener.
func (m *memListen) Accept() (net.Conn, error) {
	c, ok := <-m.listen
	if !ok {
		return nil, errors.New("closed")
	}
	return c, nil
}

// Close closes the listener.
// Any blocked Accept operations will be unblocked and return errors.
func (m *memListen) Close() error {
	close(m.listen)
	return nil
}

// Addr returns the listener's network address.
func (m *memListen) Addr() net.Addr {
	return &net.IPAddr{}
}

func (m *memListen) Dial() (net.Conn, error) {
	in, out := net.Pipe()
	m.listen <- in
	return out, nil
}

type memDialer struct {
	ll *fasthttputil.InmemoryListener
	ml *memListen
}

func (m memDialer) Dial(network string, address string) (net.Conn, error) {
	host, port, _ := net.SplitHostPort(address)
	if host == "127.0.0.1" && port == "443" {
		return nil, errors.New("tls not supported")
	}

	return m.ml.Dial()
}

func (m memDialer) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	host, port, _ := net.SplitHostPort(address)
	if (host == "127.0.0.1" || host == "::1") && port == "443" {
		return nil, errors.New("tls not supported :)")
	}
	if host != "127.0.0.1" && host != "::1" {
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}

	return m.ml.Dial()
}

func (s *server) Dialer() netns.Dialer {
	return memDialer{ll: s.ll, ml: s.ml}
}

var ErrNoCapabilityVersion = errors.New("no capability version set")

func parseCabailityVersion(req *http.Request) (tailcfg.CapabilityVersion, error) {
	clientCapabilityStr := req.URL.Query().Get("v")

	if clientCapabilityStr == "" {
		return 0, ErrNoCapabilityVersion
	}

	clientCapabilityVersion, err := strconv.Atoi(clientCapabilityStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse capability version: %w", err)
	}

	return tailcfg.CapabilityVersion(clientCapabilityVersion), nil
}

const NoiseCapabilityVersion = 39

func (s *server) KeyHandler(
	writer http.ResponseWriter,
	req *http.Request,
) {
	// New Tailscale clients send a 'v' parameter to indicate the CurrentCapabilityVersion
	capVer, err := parseCabailityVersion(req)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.logger.Info("got key request")

	// TS2021 (Tailscale v2 protocol) requires to have a different key
	if capVer >= NoiseCapabilityVersion {
		resp := tailcfg.OverTLSPublicKeyResponse{
			PublicKey: s.noisePrivateKey.Public(),
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		err = json.NewEncoder(writer).Encode(resp)
		if err != nil {
			s.logger.Error("failed to write key response", "err", err)
		}
		return
	}
}

func (s *server) NoiseUpgradeHandler(w http.ResponseWriter, r *http.Request) {
	s.logger.Info("got noise upgrade request")
	ns := noiseServer{
		logger:     s.logger,
		derpMap:    s.derpMap,
		challenge:  key.NewChallenge(),
		updates:    s.peerMapUpdate,
		node:       &s.node,
		nodeUpdate: s.nodeUpdate,
		getIP:      s.overlay.IP,
	}

	noiseConn, err := controlhttp.AcceptHTTP(
		r.Context(),
		w,
		r,
		s.noisePrivateKey,
		// ns.earlyNoise,
		// TODO: for some reason when using an unbuffered network connection
		// (such as our in-memory connection to the tsserver), the client will
		// just hang on https://github.com/coadler/tailscale/blob/main/internal/noiseconn/conn.go#L59
		// and https://github.com/coadler/tailscale/blob/main/control/controlhttp/server.go#L107.
		// Disabling the early write seems to fix it. Using a buffered network
		// connection (such as *fasthttputil.InmemoryListener), works somewhat
		// but still causes random race conditions that causes the connection to
		// stall.
		nil,
	)
	if err != nil {
		// http.Error(w, err.Error(), http.StatusInternalServerError)
		s.logger.Error("failed to accept control http", "err", err)
		return
	}
	s.logger.Info("accepted control http")

	ns.conn = noiseConn
	ns.machineKey = ns.conn.Peer()
	ns.protocolVersion = ns.conn.ProtocolVersion()

	// This router is served only over the Noise connection, and exposes only the new API.
	//
	// The HTTP2 server that exposes this router is created for
	// a single hijacked connection from /ts2021, using netutil.NewOneConnListener

	rtr := chi.NewMux()
	rtr.NotFound(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.logger.Info("ts2021 not found", "path", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	rtr.Post("/machine/register", ns.NoiseRegistrationHandler)
	rtr.HandleFunc("/machine/map", ns.NoisePollNetMapHandler)
	rtr.Post("/machine/update-health", func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		w.WriteHeader(http.StatusNoContent)
	})

	ns.httpBaseConfig = &http.Server{
		Handler:           h2c.NewHandler(rtr, ns.http2Server),
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       30 * time.Second,
	}
	ns.http2Server = &http2.Server{}

	ns.http2Server.ServeConn(
		noiseConn,
		&http2.ServeConnOpts{
			BaseConfig: ns.httpBaseConfig,
		},
	)
}

type updateType int

const (
	updateTypeNewPeer updateType = iota
	updateTypePeerUpdate
)

type update struct {
	ty updateType

	node   *tailcfg.Node
	update *tailcfg.PeerChange
}

type noiseServer struct {
	logger         *slog.Logger
	httpBaseConfig *http.Server
	http2Server    *http2.Server
	conn           *controlbase.Conn
	machineKey     key.MachinePublic
	nodeKey        key.NodePublic
	derpMap        *tailcfg.DERPMap
	getIP          func() netip.Addr

	updates    chan update
	node       *atomic.Pointer[tailcfg.Node]
	nodeUpdate chan struct{}

	// EarlyNoise-related stuff
	challenge       key.ChallengePrivate
	protocolVersion int
}

func maskUUID(uid uuid.UUID) uuid.UUID {
	// This is Tailscale's ephemeral service prefix. This can be changed easily
	// later-on, because all of our nodes are ephemeral.
	// fd7a:115c:a1e0
	uid[0] = 0xfd
	uid[1] = 0x7a
	uid[2] = 0x11
	uid[3] = 0x5c
	uid[4] = 0xa1
	uid[5] = 0xe0
	return uid
}

// IP generates a random IP with a static service prefix.
func IP() netip.Addr {
	uid := maskUUID(uuid.New())
	return netip.AddrFrom16(uid)
}

// func IP4r() netip.Addr {
// 	return netip.AddrFrom4([4]byte{100, 64, 1, 1})
// }
// func IP4s() netip.Addr {
// 	return netip.AddrFrom4([4]byte{100, 64, 2, 2})
// }

// IP generates a new IP from a UUID.
func IPFromUUID(uid uuid.UUID) netip.Addr {
	return netip.AddrFrom16(maskUUID(uid))
}

func (ns *noiseServer) notifyUpdate() {
	ns.nodeUpdate <- struct{}{}
}

func (ns *noiseServer) NoiseRegistrationHandler(w http.ResponseWriter, r *http.Request) {
	ns.logger.Info("got noise registration request")

	registerRequest := tailcfg.RegisterRequest{}
	err := json.NewDecoder(r.Body).Decode(&registerRequest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sp := strings.SplitN(registerRequest.Auth.AuthKey, "-", 2)

	ip := ns.getIP()
	// var ip netip.Addr
	// switch registerRequest.Auth.AuthKey {
	// case "receive":
	// 	ip = IP4r()
	// case "send":
	// 	ip = IP4s()
	// default:
	// 	http.Error(w, fmt.Sprintf("unknown authkey: %q", registerRequest.Auth.AuthKey), http.StatusBadRequest)
	// 	return
	// }
	// try insecureskipverify

	resp := tailcfg.RegisterResponse{}
	resp.MachineAuthorized = true
	resp.User = tailcfg.User{
		ID:          tailcfg.UserID(123),
		LoginName:   "wgsh",
		DisplayName: "wgsh",
		Logins:      []tailcfg.LoginID{},
		Created:     time.Now(),
	}
	resp.Login = tailcfg.Login{
		ID:          tailcfg.LoginID(123),
		LoginName:   "wgsh",
		DisplayName: "wgsh",
	}

	ns.nodeKey = registerRequest.NodeKey

	nodeID := tailcfg.NodeID(rand.Int64())

	addr := netip.PrefixFrom(ip, 32)

	ns.storeNode(&tailcfg.Node{
		ID:         nodeID,
		StableID:   tailcfg.StableNodeID(sp[0]),
		Hostinfo:   registerRequest.Hostinfo.View(),
		Name:       registerRequest.Hostinfo.Hostname,
		User:       resp.User.ID,
		Machine:    ns.machineKey,
		Key:        registerRequest.NodeKey,
		LastSeen:   ptr.To(time.Now()),
		Cap:        registerRequest.Version,
		Addresses:  []netip.Prefix{addr},
		AllowedIPs: []netip.Prefix{addr},
		CapMap: tailcfg.NodeCapMap{
			tailcfg.CapabilityDebug: []tailcfg.RawMessage{"true"},
		},
		MachineAuthorized: true,
	})

	ns.logger.Info("notify update")
	ns.notifyUpdate()
	ns.logger.Info("notify update done")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(resp)
	if err != nil {
		ns.logger.Error("failed to write register response", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ns.logger.Info("finished registration")
}

func (ns *noiseServer) storeNode(node *tailcfg.Node) *tailcfg.Node {
	ns.node.Store(node)
	return node
}

func (ns *noiseServer) getSelfNode() *tailcfg.Node {
	return ns.node.Load()
}

func (ns *noiseServer) NoisePollNetMapHandler(
	w http.ResponseWriter,
	req *http.Request,
) {
	ns.logger.Info("got noise poll request")

	mapRequest := tailcfg.MapRequest{}
	err := json.NewDecoder(req.Body).Decode(&mapRequest)
	if err != nil {
		ns.logger.Error("failed to decode map request", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	node := ns.getSelfNode()

	if node == nil {
		ns.logger.Info("noise poll request before registration")
		http.Error(w, "node is nil", http.StatusUnauthorized)
		return
	}

	switch parseMapRequestType(&mapRequest) {
	case mapRequestUnknown:
		ns.logger.Error("unknown map request type")
		http.Error(w, "unknown request type", http.StatusBadRequest)
		return

	case mapRequestStreaming:
		ns.logger.Info("streaming")
		ns.handleStreaming(req.Context(), w, &mapRequest)

	case mapRequestEndpointUpdate:
		ns.logger.Info("endpoint update")
		ns.handleEndpointUpdate(w, &mapRequest)
	}

}

func (ns *noiseServer) peerMap() []*tailcfg.Node {
	peers := []*tailcfg.Node{}

	// TOOD: get peers

	return peers
}

func (ns *noiseServer) handleStreaming(ctx context.Context, w http.ResponseWriter, req *tailcfg.MapRequest) {
	// Upgrade the writer to a ResponseController
	rc := http.NewResponseController(w)

	// Longpolling will break if there is a write timeout,
	// so it needs to be disabled.
	rc.SetWriteDeadline(time.Time{})

	node := ns.getSelfNode()

	keepAlive := time.NewTicker(50 * time.Second)
	defer keepAlive.Stop()

	res := &tailcfg.MapResponse{
		KeepAlive:       false,
		ControlTime:     ptr.To(time.Now()),
		Node:            node,
		DERPMap:         ns.derpMap,
		CollectServices: opt.NewBool(false),
		Debug: &tailcfg.Debug{
			DisableLogTail: true,
		},
		Peers:        ns.peerMap(),
		PacketFilter: tailcfg.FilterAllowAll,
	}

	err := writeMapResponse(w, req, res)
	if err != nil {
		ns.logger.Error("write map response", "err", err)
		return
	}
	err = rc.Flush()
	if err != nil {
		ns.logger.Error("flush map response", "err", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case upd := <-ns.updates:
			res := &tailcfg.MapResponse{
				KeepAlive:   false,
				ControlTime: ptr.To(time.Now()),
			}
			if upd.ty == updateTypeNewPeer {
				res.Peers = []*tailcfg.Node{upd.node}
			} else if upd.ty == updateTypePeerUpdate {
				res.PeersChangedPatch = []*tailcfg.PeerChange{upd.update}
			}

			err := writeMapResponse(w, req, res)
			if err != nil {
				ns.logger.Error("write map response", "err", err)
				return
			}
			err = rc.Flush()
			if err != nil {
				ns.logger.Error("flush map response", "err", err)
				return
			}

		case <-keepAlive.C:
			err := writeMapResponse(w, req, &tailcfg.MapResponse{
				KeepAlive:   true,
				ControlTime: ptr.To(time.Now()),
			})
			if err != nil {
				ns.logger.Error("write map keep alive", "err", err)
				return
			}
			err = rc.Flush()
			if err != nil {
				ns.logger.Error("flush map response", "err", err)
				return
			}
		}
	}

}

func writeMapResponse(w http.ResponseWriter, req *tailcfg.MapRequest, res *tailcfg.MapResponse) error {
	jsonBody, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("marshal map response: %w", err)
	}

	var respBody []byte
	if req.Compress == "zstd" {
		respBody = zstdEncode(jsonBody)
	} else {
		respBody = jsonBody
	}

	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, uint32(len(respBody)))
	data = append(data, respBody...)

	_, err = w.Write(data)
	if err != nil {
		return fmt.Errorf("write map response: %w", err)
	}
	return nil
}

func zstdEncode(in []byte) []byte {
	encoder, ok := zstdEncoderPool.Get().(*zstd.Encoder)
	if !ok {
		panic("invalid type in sync pool")
	}
	out := encoder.EncodeAll(in, nil)
	_ = encoder.Close()
	zstdEncoderPool.Put(encoder)

	return out
}

var zstdEncoderPool = &sync.Pool{
	New: func() any {
		encoder, err := smallzstd.NewEncoder(
			nil,
			zstd.WithEncoderLevel(zstd.SpeedFastest))
		if err != nil {
			panic(err)
		}

		return encoder
	},
}

func (ns *noiseServer) handleEndpointUpdate(_ http.ResponseWriter, req *tailcfg.MapRequest) {
	node := ns.getSelfNode()

	change := peerChange(req, node)
	change.Online = ptr.To(true)
	applyPeerChange(node, change)

	_ = ns.storeNode(node)

	sendUpdate, routesChanged := hostInfoChanged(node.Hostinfo.AsStruct(), req.Hostinfo)
	node.Hostinfo = req.Hostinfo.View()
	_ = routesChanged

	if peerChangeEmpty(change) && !sendUpdate {
		return
	}

	ns.notifyUpdate()
}

func applyPeerChange(node *tailcfg.Node, change tailcfg.PeerChange) {
	if change.Key != nil {
		node.Key = *change.Key
	}

	if change.DiscoKey != nil {
		node.DiscoKey = *change.DiscoKey
	}

	if change.Online != nil {
		node.Online = change.Online
	}

	if change.Endpoints != nil {
		node.Endpoints = change.Endpoints
	}

	// This might technically not be useful as we replace
	// the whole hostinfo blob when it has changed.
	if change.DERPRegion != 0 {
		if !node.Hostinfo.Valid() {
			node.Hostinfo = (&tailcfg.Hostinfo{
				NetInfo: &tailcfg.NetInfo{
					PreferredDERP: change.DERPRegion,
				},
			}).View()
		} else if !node.Hostinfo.NetInfo().Valid() {
			hf := node.Hostinfo.AsStruct()
			hf.NetInfo = &tailcfg.NetInfo{
				PreferredDERP: change.DERPRegion,
			}
			node.Hostinfo = hf.View()
		} else {
			hf := node.Hostinfo.AsStruct()
			hf.NetInfo.PreferredDERP = change.DERPRegion
			node.Hostinfo = hf.View()
		}
		node.DERP = fmt.Sprintf("127.3.3.40:%d", change.DERPRegion)
	}

	node.LastSeen = change.LastSeen
}

func peerChangeEmpty(chng tailcfg.PeerChange) bool {
	return chng.Key == nil &&
		chng.DiscoKey == nil &&
		chng.Online == nil &&
		chng.Endpoints == nil &&
		chng.DERPRegion == 0 &&
		chng.LastSeen == nil &&
		chng.KeyExpiry == nil
}

func peerChange(req *tailcfg.MapRequest, node *tailcfg.Node) tailcfg.PeerChange {
	ret := tailcfg.PeerChange{
		NodeID: node.ID,
	}

	if node.Key.String() != req.NodeKey.String() {
		ret.Key = &req.NodeKey
	}

	if node.DiscoKey.String() != req.DiscoKey.String() {
		ret.DiscoKey = &req.DiscoKey
	}

	if node.Hostinfo.Valid() &&
		node.Hostinfo.NetInfo().Valid() &&
		req.Hostinfo != nil &&
		req.Hostinfo.NetInfo != nil &&
		node.Hostinfo.NetInfo().PreferredDERP() != req.Hostinfo.NetInfo.PreferredDERP {
		ret.DERPRegion = req.Hostinfo.NetInfo.PreferredDERP
	}

	if req.Hostinfo != nil && req.Hostinfo.NetInfo != nil {
		// If there is no stored Hostinfo or NetInfo, use
		// the new PreferredDERP.
		if !node.Hostinfo.Valid() {
			ret.DERPRegion = req.Hostinfo.NetInfo.PreferredDERP
		} else if !node.Hostinfo.NetInfo().Valid() {
			ret.DERPRegion = req.Hostinfo.NetInfo.PreferredDERP
		} else {
			// If there is a PreferredDERP check if it has changed.
			if node.Hostinfo.NetInfo().PreferredDERP() != req.Hostinfo.NetInfo.PreferredDERP {
				ret.DERPRegion = req.Hostinfo.NetInfo.PreferredDERP
			}
		}
	}

	// TODO(kradalby): Find a good way to compare updates
	ret.Endpoints = req.Endpoints

	ret.LastSeen = ptr.To(time.Now())

	return ret
}

func hostInfoChanged(old, new *tailcfg.Hostinfo) (bool, bool) {
	if old.Equal(new) {
		return false, false
	}

	// Routes
	oldRoutes := old.RoutableIPs
	newRoutes := new.RoutableIPs

	sort.Slice(oldRoutes, func(i, j int) bool {
		return comparePrefix(oldRoutes[i], oldRoutes[j]) > 0
	})
	sort.Slice(newRoutes, func(i, j int) bool {
		return comparePrefix(newRoutes[i], newRoutes[j]) > 0
	})

	if !xslices.Equal(oldRoutes, newRoutes) {
		return true, true
	}

	// Services is mostly useful for discovery and not critical,
	// except for peerapi, which is how nodes talk to eachother.
	// If peerapi was not part of the initial mapresponse, we
	// need to make sure its sent out later as it is needed for
	// Taildrop.
	// TODO(kradalby): Length comparison is a bit naive, replace.
	if len(old.Services) != len(new.Services) {
		return true, false
	}

	return false, false
}

// TODO(kradalby): Remove after go 1.23, will be in stdlib.
// Compare returns an integer comparing two prefixes.
// The result will be 0 if p == p2, -1 if p < p2, and +1 if p > p2.
// Prefixes sort first by validity (invalid before valid), then
// address family (IPv4 before IPv6), then prefix length, then
// address.
func comparePrefix(p, p2 netip.Prefix) int {
	if c := cmp.Compare(p.Addr().BitLen(), p2.Addr().BitLen()); c != 0 {
		return c
	}
	if c := cmp.Compare(p.Bits(), p2.Bits()); c != 0 {
		return c
	}
	return p.Addr().Compare(p2.Addr())
}

type mapRequestType int

const (
	mapRequestUnknown mapRequestType = iota
	mapRequestStreaming
	mapRequestEndpointUpdate
)

func parseMapRequestType(mr *tailcfg.MapRequest) mapRequestType {
	if mr.Stream {
		return mapRequestStreaming
	} else if !mr.Stream && mr.OmitPeers {
		return mapRequestEndpointUpdate
	} else {
		return mapRequestUnknown
	}
}

const (
	earlyNoiseCapabilityVersion = 49
	earlyPayloadMagic           = "\xff\xff\xffTS"
)

func (ns *noiseServer) earlyNoise(protocolVersion int, writer io.Writer) error {
	ns.logger.Info("early noise")
	if protocolVersion < earlyNoiseCapabilityVersion {
		return nil
	}

	earlyJSON, err := json.Marshal(&tailcfg.EarlyNoise{
		NodeKeyChallenge: ns.challenge.Public(),
	})
	if err != nil {
		return err
	}

	// 5 bytes that won't be mistaken for an HTTP/2 frame:
	// https://httpwg.org/specs/rfc7540.html#rfc.section.4.1 (Especially not
	// an HTTP/2 settings frame, which isn't of type 'T')
	var notH2Frame [5]byte
	copy(notH2Frame[:], earlyPayloadMagic)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(earlyJSON)))
	// These writes are all buffered by caller, so fine to do them
	// separately:
	if _, err := writer.Write(notH2Frame[:]); err != nil {
		return err
	}
	if _, err := writer.Write(lenBuf[:]); err != nil {
		return err
	}
	if _, err := writer.Write(earlyJSON); err != nil {
		return err
	}

	return nil
}
