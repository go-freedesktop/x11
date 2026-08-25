// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build unix

package x11

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// socketPair returns a connected client/server pair over a real unix-domain
// socket in a temp directory. Nothing here is faked: SCM_RIGHTS and a read
// deadline are kernel behaviour, and an in-memory pipe cannot stand in for
// either.
func socketPair(t *testing.T) (cli *net.UnixConn, srv net.Conn) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- c
	}()
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	srv = <-accepted
	if srv == nil {
		t.Fatal("the listener never accepted")
	}
	t.Cleanup(func() { _ = srv.Close() })
	return c, srv
}

// TestDialUnixOverARealSocket exercises the production transport — a real
// unix-domain socket with real SCM_RIGHTS descriptor passing — against a
// listener in this process.
func TestDialUnixOverARealSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- c
	}()

	rw, err := DialUnix(path)
	if err != nil {
		t.Fatalf("DialUnix: %v", err)
	}
	defer func() { _ = rw.Close() }()

	srv := <-accepted
	if srv == nil {
		t.Fatal("the listener never accepted")
	}
	defer func() { _ = srv.Close() }()

	if _, err := rw.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, 5)
	if _, err := srv.Read(buf); err != nil {
		t.Fatalf("server Read: %v", err)
	}
	if string(buf) != "hello" {
		t.Errorf("server read %q", buf)
	}
	if _, err := srv.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf) != "world" {
		t.Errorf("client read %q", buf)
	}

	// The whole point of this transport: a descriptor travels with a request.
	fs, ok := rw.(FDSender)
	if !ok {
		t.Fatal("DialUnix returned a transport that cannot pass descriptors")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	if err := fs.SendFD([]byte("fd!"), int(r.Fd())); err != nil {
		t.Fatalf("SendFD: %v", err)
	}
	// Read it back on the server side and prove a real descriptor arrived.
	usrv, ok := srv.(*net.UnixConn)
	if !ok {
		t.Fatal("the accepted connection is not a *net.UnixConn")
	}
	msg := make([]byte, 8)
	oob := make([]byte, syscall.CmsgSpace(4))
	n, oobn, _, _, err := usrv.ReadMsgUnix(msg, oob)
	if err != nil {
		t.Fatalf("ReadMsgUnix: %v", err)
	}
	if string(msg[:n]) != "fd!" {
		t.Errorf("message = %q", msg[:n])
	}
	scms, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(scms) != 1 {
		t.Fatalf("ParseSocketControlMessage = %v, %v", scms, err)
	}
	fds, err := syscall.ParseUnixRights(&scms[0])
	if err != nil || len(fds) != 1 {
		t.Fatalf("ParseUnixRights = %v, %v", fds, err)
	}
	_ = syscall.Close(fds[0])
}

func TestDialUnixToNowhere(t *testing.T) {
	_, err := DialUnix(filepath.Join(t.TempDir(), "not-there"))
	if err == nil || !strings.Contains(err.Error(), "dialing") {
		t.Fatalf("DialUnix reported %v, want a dial failure", err)
	}
	if !os.IsNotExist(errUnwrapAll(err)) {
		t.Errorf("the dial failure does not bottom out in a missing file: %v", err)
	}
}

// errUnwrapAll walks an error chain to its root, which is what a caller's "is
// this just a missing socket?" test does.
func errUnwrapAll(err error) error {
	for {
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return err
		}
		next := u.Unwrap()
		if next == nil {
			return err
		}
		err = next
	}
}

func TestSendFDOverAClosedSocketFails(t *testing.T) {
	cli, srv := socketPair(t)
	_ = srv.Close()
	rw := WrapUnix(cli)
	_ = rw.Close()
	if err := rw.(FDSender).SendFD([]byte("x"), 0); err == nil {
		t.Fatal("SendFD on a closed socket reported success")
	}
}

// WaitReadable is the only thing standing between a paste from a dead owner
// and a window that never comes back, so it is exercised on a REAL socket: an
// in-memory fake cannot tell "nothing arrived" from "nothing was ever going
// to".
func TestWaitReadableOnARealSocket(t *testing.T) {
	cli, srv := socketPair(t)
	rw := WrapUnix(cli).(Waiter)

	// Silence: it must report not-ready, and must do so at the timeout rather
	// than before it or long after it.
	start := time.Now()
	if rw.WaitReadable(120 * time.Millisecond) {
		t.Error("reported ready with nothing sent")
	}
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("gave up after %v, before the timeout", elapsed)
	}
	if elapsed > time.Second {
		t.Errorf("waited %v for a 120ms timeout", elapsed)
	}

	// Something to read: ready, and quickly.
	if _, err := srv.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	start = time.Now()
	if !rw.WaitReadable(2 * time.Second) {
		t.Error("not ready with data waiting")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v to notice data that was already there", d)
	}
}

// A timeout that has already passed is not a reason to block.
func TestWaitReadableZeroTimeout(t *testing.T) {
	cli, _ := socketPair(t)
	rw := WrapUnix(cli).(Waiter)
	start := time.Now()
	if rw.WaitReadable(0) {
		t.Error("reported ready on a zero timeout with nothing sent")
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("a zero timeout blocked for %v", d)
	}
}

// A closed socket is not going to become readable, and says so at once rather
// than waiting out a timeout that cannot end differently.
func TestWaitReadableClosedSocket(t *testing.T) {
	cli, srv := socketPair(t)
	_ = srv.Close()
	rw := WrapUnix(cli)
	_ = rw.Close()

	start := time.Now()
	if rw.(Waiter).WaitReadable(2 * time.Second) {
		t.Error("a closed socket reported ready")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v to notice the socket was closed", d)
	}
}

// The byte WaitReadable took off the socket to prove something arrived must be
// handed back, or every packet it peeked at is read one byte short — which is
// a desynchronised protocol stream, not a lost byte.
func TestWaitReadableGivesTheByteBack(t *testing.T) {
	cli, srv := socketPair(t)
	rw := WrapUnix(cli)

	if _, err := srv.Write([]byte("ABCDE")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !rw.(Waiter).WaitReadable(time.Second) {
		t.Fatal("data was sent but the wait said no")
	}
	// A second wait must not take another byte.
	if !rw.(Waiter).WaitReadable(time.Second) {
		t.Fatal("the peeked byte was forgotten between waits")
	}

	got := make([]byte, 5)
	if n, err := io.ReadFull(rw, got); err != nil {
		t.Fatalf("read back: %v (%d bytes)", err, n)
	}
	if string(got) != "ABCDE" {
		t.Errorf("read %q, want the whole thing; the peeked byte was dropped", got)
	}
}
