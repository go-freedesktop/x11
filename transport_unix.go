// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build unix

package x11

import (
	"fmt"
	"io"
	"net"
	"syscall"
	"time"
)

// unixTransport adapts a *net.UnixConn to a client's byte-stream transport,
// adding descriptor passing over SCM_RIGHTS. It is what lets a connection hand
// the X server a shared-memory descriptor with MIT-SHM AttachFd while every
// other request still travels as an ordinary socket write.
type unixTransport struct {
	c *net.UnixConn
	// peek holds the byte WaitReadable took off the socket to prove something
	// had arrived. Read hands it back before touching the socket again, so the
	// packet it belongs to is never seen short.
	peek []byte
}

// WrapUnix wraps a dialed *net.UnixConn as an fd-passing transport for
// [Handshake]. The result implements [FDSender] and [Waiter].
func WrapUnix(c *net.UnixConn) io.ReadWriteCloser { return &unixTransport{c: c} }

// DialUnix connects to the X server's unix-domain socket at path and returns
// it wrapped by [WrapUnix].
func DialUnix(path string) (io.ReadWriteCloser, error) {
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("x11: dialing %s: %w", path, err)
	}
	return WrapUnix(c), nil
}

func (u *unixTransport) Read(b []byte) (int, error) {
	if len(u.peek) > 0 {
		n := copy(b, u.peek)
		u.peek = u.peek[n:]
		return n, nil
	}
	return u.c.Read(b)
}

func (u *unixTransport) Write(b []byte) (int, error) { return u.c.Write(b) }
func (u *unixTransport) Close() error                { return u.c.Close() }

// SendFD writes msg with fd attached as a single SCM_RIGHTS control message.
func (u *unixTransport) SendFD(msg []byte, fd int) error {
	oob := syscall.UnixRights(fd)
	_, _, err := u.c.WriteMsgUnix(msg, oob, nil)
	return err
}

// WaitReadable reports whether the server sent anything within d.
//
// It reads ONE byte and keeps it, rather than asking the kernel whether the
// socket is readable. That is the difference between a wait and a broken
// connection: a read deadline that expires between a packet's header and its
// body leaves the protocol stream desynchronised, whereas a byte taken off the
// front and handed back by the next Read cannot cut a packet in half.
func (u *unixTransport) WaitReadable(d time.Duration) bool {
	if len(u.peek) > 0 {
		return true
	}
	if err := u.c.SetReadDeadline(time.Now().Add(d)); err != nil {
		return false // a closed connection is not going to become readable
	}
	defer func() { _ = u.c.SetReadDeadline(time.Time{}) }()
	var one [1]byte
	n, err := u.c.Read(one[:])
	if n > 0 {
		u.peek = append(u.peek, one[0])
	}
	return err == nil && n > 0
}
