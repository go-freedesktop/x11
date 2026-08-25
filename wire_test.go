// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

package x11

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestPad4(t *testing.T) {
	for _, tc := range []struct{ in, pad, want int }{
		{0, 0, 0}, {1, 3, 4}, {2, 2, 4}, {3, 1, 4}, {4, 0, 4},
		{5, 3, 8}, {7, 1, 8}, {8, 0, 8}, {17, 3, 20},
	} {
		if got := Pad4(tc.in); got != tc.want {
			t.Errorf("Pad4(%d) = %d, want %d", tc.in, got, tc.want)
		}
		if got := Padding(tc.in); got != tc.pad {
			t.Errorf("Padding(%d) = %d, want %d", tc.in, got, tc.pad)
		}
	}
}

func TestEncoderBothOrders(t *testing.T) {
	for _, order := range []ByteOrder{binary.LittleEndian, binary.BigEndian} {
		e := NewEncoder(order)
		if e.Order() != order {
			t.Errorf("%v: Order() = %v", order, e.Order())
		}
		e.Put8(0xa1)
		e.Put16(0x1234)
		e.Put32(0xdeadbeef)
		e.PutBytes([]byte{1, 2})
		e.PutString("abc") // 3 bytes + 1 pad
		e.Skip(2)
		e.Pad(1) // 3 zero bytes, the padding that follows one written byte

		want := []byte{0xa1}
		var b2 [2]byte
		order.PutUint16(b2[:], 0x1234)
		want = append(want, b2[:]...)
		var b4 [4]byte
		order.PutUint32(b4[:], 0xdeadbeef)
		want = append(want, b4[:]...)
		want = append(want, 1, 2, 'a', 'b', 'c', 0, 0, 0, 0, 0, 0)
		if !bytes.Equal(e.Bytes(), want) {
			t.Errorf("%v: encoded % x, want % x", order, e.Bytes(), want)
		}
	}
}

func TestEncoderPadOnAlignedWritesNothing(t *testing.T) {
	e := NewEncoder(binary.LittleEndian)
	e.PutString("abcd") // already aligned: no pad
	if len(e.Bytes()) != 4 {
		t.Fatalf("aligned PutString wrote %d bytes, want 4", len(e.Bytes()))
	}
}

func TestDecoderReadsWhatEncoderWrote(t *testing.T) {
	order := binary.BigEndian
	e := NewEncoder(order)
	e.Put8(9)
	e.Put16(0xfffe) // reads back as -2 signed
	e.Put32(7)
	e.PutString("hi")
	e.PutBytes([]byte{4, 5, 6})

	d := NewDecoder(order, e.Bytes())
	if d.Order() != order {
		t.Errorf("Order() = %v", d.Order())
	}
	if got := d.Get8(); got != 9 {
		t.Errorf("Get8 = %d", got)
	}
	if got := d.Get16s(); got != -2 {
		t.Errorf("Get16s = %d, want -2", got)
	}
	if got := d.Get32(); got != 7 {
		t.Errorf("Get32 = %d", got)
	}
	if got := d.GetString(2); got != "hi" {
		t.Errorf("GetString = %q", got)
	}
	if got := d.GetBytes(3); !bytes.Equal(got, []byte{4, 5, 6}) {
		t.Errorf("GetBytes = % x", got)
	}
	if !d.OK() {
		t.Error("decoder went not-OK on a well-formed buffer")
	}
}

func TestDecoderTruncationIsSticky(t *testing.T) {
	d := NewDecoder(binary.LittleEndian, []byte{1, 2})
	if got := d.Get32(); got != 0 {
		t.Errorf("short Get32 = %d, want 0", got)
	}
	if d.OK() {
		t.Fatal("decoder stayed OK after a short read")
	}
	// Once not-OK, every subsequent read is a zero and never a panic.
	if d.Get8() != 0 || d.Get16() != 0 || d.Get32() != 0 {
		t.Error("reads after truncation did not all report zero")
	}
	if d.GetBytes(1) != nil {
		t.Error("GetBytes after truncation returned data")
	}
	if d.GetString(1) != "" {
		t.Error("GetString after truncation returned data")
	}
	d.Skip(1)
	if d.OK() {
		t.Error("Skip resurrected a not-OK decoder")
	}
}

// A length taken from the wire and widened to int can be negative. Letting it
// through would reach make([]byte, n) and panic, so the bound check refuses it
// rather than treating it as a backwards seek.
func TestDecoderRefusesANegativeLength(t *testing.T) {
	d := NewDecoder(binary.LittleEndian, []byte{1, 2, 3, 4})
	if b := d.GetBytes(-1); b != nil {
		t.Fatalf("GetBytes(-1) returned % x", b)
	}
	if d.OK() {
		t.Fatal("a negative length left the decoder OK")
	}
}

func TestDecoderSkipPastTheEnd(t *testing.T) {
	d := NewDecoder(binary.LittleEndian, []byte{1, 2})
	d.Skip(9)
	if d.OK() {
		t.Fatal("Skip past the end left the decoder OK")
	}
}

func TestDecoderShortReadsPerWidth(t *testing.T) {
	for _, n := range []int{0, 1, 3} {
		d := NewDecoder(binary.LittleEndian, make([]byte, n))
		d.Get16()
		d.Get32()
		d.Get8()
		d.Get8() // exercises the 1-byte short path for n == 0 and n == 1
	}
}

func TestOrderFor(t *testing.T) {
	if o, ok := OrderFor(OrderLSB); !ok || o != binary.ByteOrder(binary.LittleEndian) {
		t.Errorf("OrderFor('l') = %v, %v", o, ok)
	}
	if o, ok := OrderFor(OrderMSB); !ok || o != binary.ByteOrder(binary.BigEndian) {
		t.Errorf("OrderFor('B') = %v, %v", o, ok)
	}
	if _, ok := OrderFor('x'); ok {
		t.Error("OrderFor('x') reported a known order")
	}
}

func TestReadFull(t *testing.T) {
	b := make([]byte, 4)
	if err := ReadFull(bytes.NewReader([]byte{1, 2, 3, 4}), b); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	err := ReadFull(bytes.NewReader([]byte{1}), b)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short ReadFull reported %v, want ErrUnexpectedEOF", err)
	}
}

func TestTrimNul(t *testing.T) {
	for _, tc := range []struct {
		in   []byte
		want string
	}{
		{nil, ""},
		{[]byte("plain"), "plain"},
		{[]byte("cut\x00off"), "cut"},
		{[]byte("\x00"), ""},
	} {
		if got := TrimNul(tc.in); got != tc.want {
			t.Errorf("TrimNul(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
