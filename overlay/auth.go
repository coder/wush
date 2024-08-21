package overlay

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"

	"github.com/btcsuite/btcd/btcutil/base58"
	"go4.org/mem"
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
	dec := cursor{
		b: base58.Decode(authKey),
	}

	if len(authKey) == 0 {
		return errors.New("auth key should not be empty")
	}

	ipLen := int(dec.next(1)[0])
	if ipLen > 0 {
		stunIPBytes := dec.next(ipLen + 2)
		err := ca.ReceiverStunAddr.UnmarshalBinary(stunIPBytes)
		if err != nil {
			return fmt.Errorf("unmarshal receiver stun address: %w", err)
		}
	}

	ca.ReceiverDERPRegionID = binary.BigEndian.Uint16(dec.next(2))

	ca.ReceiverPublicKey = key.NodePublicFromRaw32(mem.B(dec.next(32)))
	ca.OverlayPrivateKey = key.NodePrivateFromRaw32(mem.B(dec.next(32)))
	return nil
}

type cursor struct {
	at int
	b  []byte
}

func (c *cursor) next(i int) []byte {
	ret := c.b[c.at : c.at+i]
	c.at += i
	return ret
}
