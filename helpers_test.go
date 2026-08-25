// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

package x11

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

// errInjected is the sentinel every forced-failure test looks for, so a test
// that expects a failure cannot be satisfied by an unrelated real one.
var errInjected = errors.New("injected")

// fakeTransport is an in-memory io.ReadWriteCloser: reads drain a preloaded
// server→client script, writes accumulate for inspection. It is what makes the
// setup exchange testable on a platform with no X server and no unix socket.
type fakeTransport struct {
	in       *bytes.Reader // server -> client
	out      bytes.Buffer  // client -> server (captured)
	closed   bool
	writeErr error // when set, every Write fails with it
	readErr  error // when set, Read fails with it once the script has drained
}

func newFakeTransport(serverBytes []byte) *fakeTransport {
	return &fakeTransport{in: bytes.NewReader(serverBytes)}
}

func (f *fakeTransport) Read(p []byte) (int, error) {
	if f.in.Len() == 0 && f.readErr != nil {
		return 0, f.readErr
	}
	return f.in.Read(p)
}

func (f *fakeTransport) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.out.Write(p)
}

func (f *fakeTransport) Close() error { f.closed = true; return nil }

var _ io.ReadWriteCloser = (*fakeTransport)(nil)

// setupSpec, screenSpec and depthSpec describe a setup reply to build.
type setupSpec struct {
	Release        uint32
	ResourceIDBase uint32
	ResourceIDMask uint32
	Vendor         string
	MaxRequestLen  uint16
	ImageByteOrder uint8
	Formats        []Format
	Screens        []screenSpec
}

type screenSpec struct {
	Root       uint32
	Colormap   uint32
	White      uint32
	Black      uint32
	Width      uint16
	Height     uint16
	WidthMM    uint16
	HeightMM   uint16
	RootVisual uint32
	RootDepth  uint8
	Depths     []depthSpec
}

type depthSpec struct {
	Depth   uint8
	Visuals []VisualType
}

// buildSetupBody encodes a setup reply body. It is the exact inverse of
// parseSetupReply, so a round-trip through it proves the parser against a
// description a human can read rather than against a byte blob.
func buildSetupBody(order ByteOrder, s setupSpec) []byte {
	e := NewEncoder(order)
	e.Put32(s.Release)
	e.Put32(s.ResourceIDBase)
	e.Put32(s.ResourceIDMask)
	e.Put32(256) // motion-buffer-size
	e.Put16(uint16(len(s.Vendor)))
	max := s.MaxRequestLen
	if max == 0 {
		max = 65535
	}
	e.Put16(max)
	e.Put8(uint8(len(s.Screens)))
	e.Put8(uint8(len(s.Formats)))
	e.Put8(s.ImageByteOrder)
	e.Put8(0)  // bitmap-bit-order
	e.Put8(32) // bitmap-format-scanline-unit
	e.Put8(32) // bitmap-format-scanline-pad
	e.Put8(8)  // min-keycode
	e.Put8(255)
	e.Skip(4) // unused
	e.PutString(s.Vendor)
	for _, f := range s.Formats {
		e.Put8(f.Depth)
		e.Put8(f.BitsPerPix)
		e.Put8(f.ScanlinePad)
		e.Skip(5)
	}
	for _, sc := range s.Screens {
		e.Put32(sc.Root)
		e.Put32(sc.Colormap)
		e.Put32(sc.White)
		e.Put32(sc.Black)
		e.Put32(0) // current-input-masks
		e.Put16(sc.Width)
		e.Put16(sc.Height)
		e.Put16(sc.WidthMM)
		e.Put16(sc.HeightMM)
		e.Put16(1) // min-installed-maps
		e.Put16(1) // max-installed-maps
		e.Put32(sc.RootVisual)
		e.Put8(0) // backing-stores
		e.Put8(0) // save-unders
		e.Put8(sc.RootDepth)
		e.Put8(uint8(len(sc.Depths)))
		for _, dp := range sc.Depths {
			e.Put8(dp.Depth)
			e.Skip(1)
			e.Put16(uint16(len(dp.Visuals)))
			e.Skip(4)
			for _, v := range dp.Visuals {
				e.Put32(v.ID)
				e.Put8(v.Class)
				e.Put8(v.BitsPerRGB)
				e.Put16(v.ColormapEnt)
				e.Put32(v.RedMask)
				e.Put32(v.GreenMask)
				e.Put32(v.BlueMask)
				e.Skip(4)
			}
		}
	}
	return e.Bytes()
}

// defaultSetupBody describes a plausible modern server: one screen, 1920x1080,
// depth 24 TrueColor at 32 bits per pixel with 32-bit scanline padding.
func defaultSetupBody(order ByteOrder) []byte {
	return buildSetupBody(order, setupSpec{
		Vendor:         "fake",
		ResourceIDBase: 0x200000,
		ResourceIDMask: 0x1fffff,
		Formats: []Format{
			{Depth: 1, BitsPerPix: 1, ScanlinePad: 32},
			{Depth: 24, BitsPerPix: 32, ScanlinePad: 32},
		},
		Screens: []screenSpec{{
			Root: 0x100, Width: 1920, Height: 1080, WidthMM: 508, HeightMM: 285,
			RootVisual: 0x21, RootDepth: 24,
			Depths: []depthSpec{{Depth: 24, Visuals: []VisualType{{
				ID: 0x21, Class: VisualTrueColor, BitsPerRGB: 8, ColormapEnt: 256,
				RedMask: 0x00ff0000, GreenMask: 0x0000ff00, BlueMask: 0x000000ff,
			}}}},
		}},
	})
}

// setupPacket frames a whole setup reply: the 8-byte header plus body.
func setupPacket(order ByteOrder, status byte, body []byte, reasonLen byte) []byte {
	hdr := make([]byte, 8)
	hdr[0] = status
	hdr[1] = reasonLen
	order.PutUint16(hdr[2:4], 11)
	order.PutUint16(hdr[4:6], 0)
	order.PutUint16(hdr[6:8], uint16(len(body)/4))
	return append(hdr, body...)
}

var _ = binary.LittleEndian
