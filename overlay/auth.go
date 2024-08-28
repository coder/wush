package overlay

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"

	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/coder/wush/cliui"
	"go4.org/mem"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

type ClientAuth struct {
	// OverlayPrivateKey is the main auth mechanism used to secure the overlay.
	// Peers are sent this private key to encrypt node communication to the
	// receiver. Leaking this private key would allow anyone to connect.
	OverlayPrivateKey key.NodePrivate
	// ReceiverPublicKey is the public key of the receiver. Node messages are
	// encrypted to this public key.
	ReceiverPublicKey key.NodePublic
	// ReceiverStunAddr is the address that the receiver is reachable over UDP
	// when the overlay is running in P2P mode.
	ReceiverStunAddr netip.AddrPort
	// ReceiverDERPRegionID is the region id that the receiver is reachable over
	// DERP when the overlay is running in DERP mode.
	ReceiverDERPRegionID uint16
}

func (ca *ClientAuth) PrintDebug(logf func(str string, args ...any), dm *tailcfg.DERPMap) {
	logf("Auth information:")
	stunStr := ca.ReceiverStunAddr.String()
	if !ca.ReceiverStunAddr.IsValid() {
		stunStr = "Disabled"
	}
	logf("\t> Server overlay STUN address: %s", cliui.Code(stunStr))
	derpStr := "Disabled"
	if ca.ReceiverDERPRegionID > 0 {
		derpStr = dm.Regions[int(ca.ReceiverDERPRegionID)].RegionName
	}
	logf("\t> Server overlay DERP home:    %s", cliui.Code(derpStr))
	logf("\t> Server overlay public key:   %s", cliui.Code(ca.ReceiverPublicKey.ShortString()))
	logf("\t> Server overlay auth key:     %s", cliui.Code(ca.OverlayPrivateKey.Public().ShortString()))
}

func (ca *ClientAuth) AuthKey() string {
	buf := bytes.NewBuffer(nil)

	buf.WriteByte(byte(ca.ReceiverStunAddr.Addr().BitLen() / 8))
	if ca.ReceiverStunAddr.Addr().BitLen() > 0 {
		stunBytes, err := ca.ReceiverStunAddr.MarshalBinary()
		if err != nil {
			panic(fmt.Sprint("failed to marshal stun addr:", err))
		}
		buf.Write(stunBytes)
	}

	derpBuf := [2]byte{}
	binary.BigEndian.PutUint16(derpBuf[:], ca.ReceiverDERPRegionID)
	buf.Write(derpBuf[:])

	pub := ca.ReceiverPublicKey.Raw32()
	buf.Write(pub[:])

	priv := ca.OverlayPrivateKey.Raw32()
	buf.Write(priv[:])

	return base58.Encode(buf.Bytes())
}

func (ca *ClientAuth) Parse(authKey string) error {
	if len(authKey) == 0 {
		return errors.New("auth key should not be empty")
	}

	decr := bytes.NewReader(base58.Decode(authKey))

	ipLenB, err := decr.ReadByte()
	if err != nil {
		return errors.New("read STUN ip len; invalid authkey")
	}

	ipLen := int(ipLenB)
	if ipLen > 0 {
		stunIPBytes := make([]byte, ipLen+2)
		n, err := decr.Read(stunIPBytes)
		if n != len(stunIPBytes) || err != nil {
			return errors.New("read STUN ip; invalid authkey")
		}

		err = ca.ReceiverStunAddr.UnmarshalBinary(stunIPBytes)
		if err != nil {
			return fmt.Errorf("unmarshal receiver stun address: %w", err)
		}
	}

	derpRegionBytes := make([]byte, 2)
	n, err := decr.Read(derpRegionBytes)
	if n != len(derpRegionBytes) || err != nil {
		return errors.New("read derp region; invalid authkey")
	}
	ca.ReceiverDERPRegionID = binary.BigEndian.Uint16(derpRegionBytes)

	pubKeyBytes := make([]byte, 32)
	n, err = decr.Read(pubKeyBytes)
	if n != len(pubKeyBytes) || err != nil {
		return errors.New("read receiver pubkey; invalid authkey")
	}
	ca.ReceiverPublicKey = key.NodePublicFromRaw32(mem.B(pubKeyBytes))

	privKeyBytes := make([]byte, 32)
	n, err = decr.Read(privKeyBytes)
	if n != len(privKeyBytes) || err != nil {
		return errors.New("read overlay privkey; invalid authkey")
	}
	ca.OverlayPrivateKey = key.NodePrivateFromRaw32(mem.B(privKeyBytes))
	return nil
}
