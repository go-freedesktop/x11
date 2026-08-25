# x11 — go-freedesktop

[![ci](https://github.com/go-freedesktop/x11/actions/workflows/ci.yml/badge.svg)](https://github.com/go-freedesktop/x11/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-freedesktop/x11.svg)](https://pkg.go.dev/github.com/go-freedesktop/x11)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26.4%2B-00ADD8)](https://go.dev/dl/)
[![Coverage](https://img.shields.io/badge/coverage-100%25-1a7f37)](#tests--coverage)

The **foundation every X11 client needs before it can say anything of its
own** — the wire codec, the `.Xauthority` parser, the connection-setup
exchange, the anonymous shared-memory segment MIT-SHM attaches, and the
unix-domain transport that hands the server a descriptor over `SCM_RIGHTS`.

Pure Go, **CGO-free, standard library only**. No Xlib, no XCB, no `cgo` — the
wire format is encoded and decoded here, byte for byte, per the X11 protocol
specification.

## Scope — deliberately, the bytes and nothing above them

This package stops at the byte stream. It has **no request table, no event
loop and no opinion about what a client does with a connection**, because the
two things clients do with one — pump events for a window, or pull frames for a
capture — want genuinely different request/reply machines over the same socket.
Forcing them to share one would make both worse. A consumer builds its own
connection type over [`Handshake`]'s result.

| in | out |
|---|---|
| `Encoder` / `Decoder` — the wire codec, both byte orders, every read bounds-checked | request/reply/event demultiplexing |
| `LoadAuthCookie`, `ParseXauthority` — MIT-MAGIC-COOKIE-1 from `$XAUTHORITY` | window creation, mapping, properties |
| `Handshake` + `Setup` — the setup exchange and its full reply (formats, screens, depths, visuals) | keysym / keyboard mapping |
| `Segment` — an anonymous shared-memory region, mapped, for MIT-SHM | the MIT-SHM, RANDR, XFIXES, Present request encodings |
| `WrapUnix` / `DialUnix` — the unix transport, `SCM_RIGHTS` fd passing, readability waiting | `DISPLAY` parsing and socket-path search |

## Transport-agnostic on purpose

`Handshake` takes any `io.ReadWriteCloser`. In production that is a dialed unix
socket; in a test it is one half of a `net.Pipe` driven by a scripted fake
server. That is not a convenience — it is what lets the **entire codec be
tested on darwin and on windows, with no X server anywhere**, so a protocol bug
is caught on every platform rather than only on the one that can run an X
server.

```go
rw, err := x11.DialUnix("/tmp/.X11-unix/X0")
if err != nil {
    return err
}
name, data, err := x11.LoadAuthCookie(x11.AuthFilePath(), "", "0")
if err != nil {
    return err
}
setup, err := x11.Handshake(rw, binary.LittleEndian, name, data)
if err != nil {
    return err
}
// setup.Screens[0].RootVisualType() and setup.FormatFor(depth) are now
// everything you need to size and decode an image. Frame your own requests
// over rw from here.
```

## Descriptor passing, and why it is here

MIT-SHM 1.2's `AttachFd` hands the X server a descriptor for a shared segment,
so a full-screen image costs a ~40-byte request instead of megabytes down the
socket. That needs two things that are not protocol: a segment (`NewSegment` —
an unlinked file on a tmpfs, mapped `MAP_SHARED`, the portable equivalent of
`memfd_create`) and a transport that can carry a descriptor (`WrapUnix`, whose
result implements `FDSender`).

Which MIT-SHM requests a client then sends over them is the client's business
and lives in the client: a capture asks the server to *write* into the segment,
a window writes into it and asks the server to *read*.

Off Linux there is no segment to make: `NewSegment` reports
`ErrNoSharedMemory`, and everything above it still builds and still passes.

## Platforms

| | |
|---|---|
| **linux** (amd64, arm64, riscv64, loong64, ppc64le, s390x) | everything, against real syscalls |
| **darwin, freebsd, other unix** | codec + xauth + setup + the unix transport; no shared memory |
| **windows, js/wasm** | codec + xauth + setup; `DialUnix` reports `ErrNoTransport` |

Both wire orders are exercised, and the big-endian path is *executed* on real
big-endian (s390x under qemu) rather than merely compiled.

## Tests & coverage

**100% of statements, on Linux, darwin and windows, with no exemptions.** Every
line is either pure protocol arithmetic or a syscall behind a package variable
a test can fail on purpose — so a number below 100 means a branch nobody has
tested, not a platform getting in the way. The gate is
[`.github/coverage-gate.sh`](.github/coverage-gate.sh) and it runs on all three.

```sh
go test -coverprofile=cover.out ./... && ./.github/coverage-gate.sh cover.out
```

## Consumers

- [`go-freedesktop/screencast`](https://github.com/go-freedesktop/screencast) — X11 screen capture (RANDR, XFIXES, MIT-SHM `GetImage`)
- [`go-widgets/window`](https://github.com/go-widgets/window) — the toolkit's X11 windowing back-end (windows, events, clipboard, MIT-SHM `PutImage`)

## License

BSD-3-Clause. See [LICENSE](LICENSE).
