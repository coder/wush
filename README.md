# wush

[![Go Reference](https://pkg.go.dev/badge/github.com/coder/wush.svg)](https://pkg.go.dev/github.com/coder/wush)

`wush` is a command line tool that lets you easily transfer
files and open shells over a peer-to-peer wireguard connection. It's similar to [magic-wormhole](https://github.com/magic-wormhole/magic-wormhole) but doesn't require you to
set up or trust a relay server.

## Basic Usage

Install:

```bash
go install github.com/coder/wush/cmd/wush@latest
```

On the host machine:

```bash
$ wush receive
Picked DERP region Toronto as overlay home
Your auth key is:
    >  112v1RyL5KPzsbMbhT7fkEGrcfpygxtnvwjR5kMLGxDHGeLTK1BvoPqsUcjo7xyMkFn46KLTdedKuPCG5trP84mz9kx
Use this key to authenticate other wush commands to this instance.
05:18:59 Wireguard is ready
05:18:59 SSH server listening
```

On the client machine:

```bash
$ wush
┃ Enter the receiver's Auth key:
┃ > 112v1RyL5KPzsbMbhT7fkEGrcfpygxtnvwjR5kMLGxDHGeLTK1BvoPqsUcjo7xyMkFn46KLTdedKuPCG5trP84mz9kx
Auth information:
    > Server overlay STUN address:  Disabled
    > Server overlay DERP home:     Toronto
    > Server overlay public key:    [sEIS1]
    > Server overlay auth key:      [w/sYF]
Bringing Wireguard up..
Wireguard is ready!
Received peer
Peer active with relay  nyc
Peer active over p2p  172.20.0.8:44483
coder@colin:~$
```

## Technical Details

...
