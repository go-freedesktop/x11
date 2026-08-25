// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package x11 is a from-scratch, pure-Go (CGO-free, no non-stdlib dependency)
// implementation of the pieces of the X Window System protocol, version 11.0,
// that every X client needs before it can say anything of its own: the wire
// encoder and decoder, the .Xauthority parser, the connection-setup exchange
// and its reply, the anonymous shared-memory segment MIT-SHM attaches, and the
// unix-domain transport that hands the server a descriptor over SCM_RIGHTS.
//
// It deliberately stops there. It has no request table, no event loop and no
// opinion about what a client does with a connection: a screen capture and a
// window toolkit want very different request/reply machines on top of the same
// bytes, and this package is the bytes. A consumer builds its own connection
// type over [Handshake]'s result.
//
// Everything above the socket is transport-agnostic: [Handshake] takes any
// io.ReadWriteCloser, so the whole codec is exercisable in-process over a
// net.Pipe against a scripted fake server. That is what lets the wire format be
// tested to 100% on darwin and on windows, with no X server anywhere — a
// protocol bug is caught on every platform, not only on the one that can run
// an X server.
package x11

import (
	"encoding/binary"
	"io"
)

// ByteOrder is the wire byte order negotiated at connection setup. X11 lets
// the client pick; the server then speaks the client's order for the session.
type ByteOrder = binary.ByteOrder

// The two byte-order sentinels sent as the first byte of the setup request:
// 'l' little-endian (LSB first), 'B' big-endian (MSB first).
const (
	OrderLSB = 'l'
	OrderMSB = 'B'
)

// Pad4 returns n rounded up to the next multiple of four. X11 pads every
// variable-length field to a four-byte boundary.
func Pad4(n int) int { return (n + 3) &^ 3 }

// Padding returns the number of pad bytes needed after n data bytes.
func Padding(n int) int { return Pad4(n) - n }

// Encoder builds a request body in a chosen byte order. Every multi-byte
// integer goes through the negotiated ByteOrder, so the same code emits a
// correct little- or big-endian stream.
type Encoder struct {
	order ByteOrder
	buf   []byte
}

// NewEncoder starts an Encoder in the given order.
func NewEncoder(order ByteOrder) *Encoder { return &Encoder{order: order} }

// Bytes returns the bytes written so far. The slice aliases the Encoder's own
// buffer, so a caller that keeps it past the next write must copy it.
func (e *Encoder) Bytes() []byte { return e.buf }

// Order returns the byte order the Encoder writes in.
func (e *Encoder) Order() ByteOrder { return e.order }

// Put8 appends one byte.
func (e *Encoder) Put8(v byte) { e.buf = append(e.buf, v) }

// Put16 appends a 16-bit value in the negotiated order.
func (e *Encoder) Put16(v uint16) {
	var b [2]byte
	e.order.PutUint16(b[:], v)
	e.buf = append(e.buf, b[:]...)
}

// Put32 appends a 32-bit value in the negotiated order.
func (e *Encoder) Put32(v uint32) {
	var b [4]byte
	e.order.PutUint32(b[:], v)
	e.buf = append(e.buf, b[:]...)
}

// PutBytes appends raw bytes verbatim (no padding).
func (e *Encoder) PutBytes(b []byte) { e.buf = append(e.buf, b...) }

// PutString appends s then pads to a four-byte boundary with zero bytes.
func (e *Encoder) PutString(s string) {
	e.buf = append(e.buf, s...)
	e.Pad(len(s))
}

// Pad appends the padding that follows n written bytes.
func (e *Encoder) Pad(n int) { e.Skip(Padding(n)) }

// Skip appends n zero bytes (for "unused" fixed fields).
func (e *Encoder) Skip(n int) {
	for i := 0; i < n; i++ {
		e.buf = append(e.buf, 0)
	}
}

// Decoder reads a fixed-order byte slice. Every read is bounds-checked; once
// the Decoder is not OK it stays that way, so a truncated buffer degrades to a
// clean error at the call site rather than a panic.
type Decoder struct {
	order ByteOrder
	buf   []byte
	off   int
	ok    bool
}

// NewDecoder wraps b for reading in the given order.
func NewDecoder(order ByteOrder, b []byte) *Decoder {
	return &Decoder{order: order, buf: b, ok: true}
}

// OK reports whether every read so far stayed inside the buffer. A parser
// checks it once, at the end, rather than after every field.
func (d *Decoder) OK() bool { return d.ok }

// Order returns the byte order the Decoder reads in.
func (d *Decoder) Order() ByteOrder { return d.order }

// need reports whether n more bytes are available, clearing ok if not. A
// negative n is refused rather than treated as a backwards seek: a length
// taken from the wire and widened to int can be negative, and letting it
// through would reach make([]byte, n) with a negative size, which panics.
func (d *Decoder) need(n int) bool {
	if !d.ok || n < 0 || d.off+n > len(d.buf) {
		d.ok = false
		return false
	}
	return true
}

// Get8 reads one byte.
func (d *Decoder) Get8() byte {
	if !d.need(1) {
		return 0
	}
	v := d.buf[d.off]
	d.off++
	return v
}

// Get16 reads a 16-bit value in the decoder's order.
func (d *Decoder) Get16() uint16 {
	if !d.need(2) {
		return 0
	}
	v := d.order.Uint16(d.buf[d.off:])
	d.off += 2
	return v
}

// Get16s reads a signed 16-bit value, which is how X11 states coordinates.
func (d *Decoder) Get16s() int16 { return int16(d.Get16()) }

// Get32 reads a 32-bit value in the decoder's order.
func (d *Decoder) Get32() uint32 {
	if !d.need(4) {
		return 0
	}
	v := d.order.Uint32(d.buf[d.off:])
	d.off += 4
	return v
}

// GetBytes returns the next n bytes (a copy) and advances.
func (d *Decoder) GetBytes(n int) []byte {
	if !d.need(n) {
		return nil
	}
	out := make([]byte, n)
	copy(out, d.buf[d.off:d.off+n])
	d.off += n
	return out
}

// GetString reads an n-byte string and skips its four-byte padding.
func (d *Decoder) GetString(n int) string {
	b := d.GetBytes(n)
	d.Skip(Padding(n))
	return string(b)
}

// Skip advances over n bytes, going not-OK rather than past the end.
func (d *Decoder) Skip(n int) {
	if !d.need(n) {
		return
	}
	d.off += n
}

// OrderFor maps a byte-order sentinel to its binary.ByteOrder. The bool
// reports whether the sentinel was recognised.
func OrderFor(sentinel byte) (ByteOrder, bool) {
	switch sentinel {
	case OrderLSB:
		return binary.LittleEndian, true
	case OrderMSB:
		return binary.BigEndian, true
	}
	return nil, false
}

// ReadFull reads exactly len(b) bytes or returns the first error. It is kept
// here so the codec's callers have one spelling of "read a whole packet".
func ReadFull(r io.Reader, b []byte) error {
	_, err := io.ReadFull(r, b)
	return err
}

// TrimNul returns b up to its first NUL, as a string. X11 zero-pads several
// text fields (a vendor string, a refusal reason, a format-8 property) and
// states their length separately or not at all.
func TrimNul(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
