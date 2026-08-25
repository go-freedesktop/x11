// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

package x11

import "fmt"

// Format is one entry of the server's pixmap-format list: for a given colour
// depth it fixes the bits-per-pixel and the scanline padding a ZPixmap image
// of that depth uses on the wire. It is what turns a width into a STRIDE.
type Format struct {
	Depth       uint8
	BitsPerPix  uint8
	ScanlinePad uint8
}

// Stride is the number of BYTES one scanline of a width-pixel ZPixmap image
// occupies in this format: the pixel bits rounded up to the format's scanline
// pad. It is not width*BitsPerPix/8 in general, which is exactly why an image
// must carry it rather than assume it.
func (f Format) Stride(width int) int {
	if width <= 0 || f.BitsPerPix == 0 || f.ScanlinePad == 0 {
		return 0
	}
	bits := width * int(f.BitsPerPix)
	pad := int(f.ScanlinePad)
	return ((bits + pad - 1) / pad) * pad / 8
}

// VisualType describes a visual: its class and the RGB channel masks a
// TrueColor or DirectColor visual packs a pixel with. The masks are what
// convert a pixel to or from any other layout.
type VisualType struct {
	ID          uint32
	Class       uint8
	BitsPerRGB  uint8
	ColormapEnt uint16
	RedMask     uint32
	GreenMask   uint32
	BlueMask    uint32
}

// Visual classes. Only TrueColor and DirectColor decompose a pixel through the
// masks; the palette classes would need the colormap read back, which no
// display built this millennium presents.
const (
	VisualStaticGray  = 0
	VisualGrayScale   = 1
	VisualStaticColor = 2
	VisualPseudoColor = 3
	VisualTrueColor   = 4
	VisualDirectColor = 5
)

// Direct reports whether the visual's pixels decompose through its masks.
func (v VisualType) Direct() bool {
	return v.Class == VisualTrueColor || v.Class == VisualDirectColor
}

// Depth groups the visuals available at a given colour depth.
type Depth struct {
	Depth   uint8
	Visuals []VisualType
}

// Screen is one root screen: its root window, size, root visual and the
// allowed depths (each carrying its visuals).
type Screen struct {
	Root          uint32
	DefaultColmap uint32
	WhitePixel    uint32
	BlackPixel    uint32
	Width         uint16
	Height        uint16
	WidthMM       uint16
	HeightMM      uint16
	RootVisual    uint32
	RootDepth     uint8
	Depths        []Depth
}

// Setup is the parsed server connection-setup reply: everything a client needs
// to allocate resource IDs, pick a visual, size images correctly and map
// keycodes.
type Setup struct {
	ProtoMajor     uint16
	ProtoMinor     uint16
	Release        uint32
	ResourceIDBase uint32
	ResourceIDMask uint32
	Vendor         string
	MaxRequestLen  uint16 // in 4-byte units
	ImageByteOrder uint8  // 0 = LSBFirst, 1 = MSBFirst
	BitmapBitOrder uint8
	BitmapUnit     uint8
	BitmapPad      uint8
	MinKeycode     uint8
	MaxKeycode     uint8
	Formats        []Format
	Screens        []Screen
}

// Image byte-order values reported by Setup.ImageByteOrder.
const (
	ImageOrderLSB = 0
	ImageOrderMSB = 1
)

// buildSetupRequest encodes the client connection-setup request: the
// byte-order sentinel, protocol 11.0, and the authorization name and data
// (MIT-MAGIC-COOKIE-1 and its 16-byte cookie, both empty for an
// unauthenticated local connection).
func buildSetupRequest(order ByteOrder, sentinel byte, authName string, authData []byte) []byte {
	e := NewEncoder(order)
	e.Put8(sentinel)
	e.Put8(0) // unused
	e.Put16(11)
	e.Put16(0)
	e.Put16(uint16(len(authName)))
	e.Put16(uint16(len(authData)))
	e.Skip(2) // unused
	e.PutString(authName)
	e.PutBytes(authData)
	e.Pad(len(authData))
	return e.Bytes()
}

// parseSetupReply decodes a Success setup reply body. The 8-byte reply header
// (status, pad, major, minor, additional-data length) has already been
// consumed by the caller; body is the additional-data block.
func parseSetupReply(order ByteOrder, body []byte) (*Setup, error) {
	d := NewDecoder(order, body)
	s := &Setup{}
	s.Release = d.Get32()
	s.ResourceIDBase = d.Get32()
	s.ResourceIDMask = d.Get32()
	_ = d.Get32() // motion-buffer-size
	vendorLen := int(d.Get16())
	s.MaxRequestLen = d.Get16()
	numScreens := int(d.Get8())
	numFormats := int(d.Get8())
	s.ImageByteOrder = d.Get8()
	s.BitmapBitOrder = d.Get8()
	s.BitmapUnit = d.Get8()
	s.BitmapPad = d.Get8()
	s.MinKeycode = d.Get8()
	s.MaxKeycode = d.Get8()
	d.Skip(4) // unused
	s.Vendor = d.GetString(vendorLen)

	s.Formats = make([]Format, 0, numFormats)
	for i := 0; i < numFormats; i++ {
		f := Format{Depth: d.Get8(), BitsPerPix: d.Get8(), ScanlinePad: d.Get8()}
		d.Skip(5) // unused
		s.Formats = append(s.Formats, f)
	}

	s.Screens = make([]Screen, 0, numScreens)
	for i := 0; i < numScreens; i++ {
		sc := Screen{}
		sc.Root = d.Get32()
		sc.DefaultColmap = d.Get32()
		sc.WhitePixel = d.Get32()
		sc.BlackPixel = d.Get32()
		_ = d.Get32() // current-input-masks
		sc.Width = d.Get16()
		sc.Height = d.Get16()
		sc.WidthMM = d.Get16()
		sc.HeightMM = d.Get16()
		_ = d.Get16() // min-installed-maps
		_ = d.Get16() // max-installed-maps
		sc.RootVisual = d.Get32()
		_ = d.Get8() // backing-stores
		_ = d.Get8() // save-unders
		sc.RootDepth = d.Get8()
		numDepths := int(d.Get8())
		sc.Depths = make([]Depth, 0, numDepths)
		for j := 0; j < numDepths; j++ {
			dp := Depth{Depth: d.Get8()}
			d.Skip(1) // unused
			numVis := int(d.Get16())
			d.Skip(4) // unused
			dp.Visuals = make([]VisualType, 0, numVis)
			for k := 0; k < numVis; k++ {
				v := VisualType{}
				v.ID = d.Get32()
				v.Class = d.Get8()
				v.BitsPerRGB = d.Get8()
				v.ColormapEnt = d.Get16()
				v.RedMask = d.Get32()
				v.GreenMask = d.Get32()
				v.BlueMask = d.Get32()
				d.Skip(4) // unused
				dp.Visuals = append(dp.Visuals, v)
			}
			sc.Depths = append(sc.Depths, dp)
		}
		s.Screens = append(s.Screens, sc)
	}

	if !d.OK() {
		return nil, fmt.Errorf("x11: truncated setup reply")
	}
	if len(s.Screens) == 0 {
		return nil, fmt.Errorf("x11: setup reply has no screens")
	}
	return s, nil
}

// FindVisual returns the VisualType with the given id on screen sc, and
// whether it was found.
func (sc *Screen) FindVisual(id uint32) (VisualType, bool) {
	for _, dp := range sc.Depths {
		for _, v := range dp.Visuals {
			if v.ID == id {
				return v, true
			}
		}
	}
	return VisualType{}, false
}

// DepthOfVisual returns the colour depth the given visual lives at, and
// whether it was found.
func (sc *Screen) DepthOfVisual(id uint32) (uint8, bool) {
	for _, dp := range sc.Depths {
		for _, v := range dp.Visuals {
			if v.ID == id {
				return dp.Depth, true
			}
		}
	}
	return 0, false
}

// RootVisualType returns the screen's root visual descriptor, falling back to
// a synthesized 24-bit TrueColor BGRX visual if the root visual id is somehow
// absent from the depth list (defensive; real servers always list it).
func (sc *Screen) RootVisualType() VisualType {
	if v, ok := sc.FindVisual(sc.RootVisual); ok {
		return v
	}
	return VisualType{
		ID:        sc.RootVisual,
		Class:     VisualTrueColor,
		RedMask:   0x00ff0000,
		GreenMask: 0x0000ff00,
		BlueMask:  0x000000ff,
	}
}

// FormatFor returns the pixmap Format matching depth, and whether one exists.
// It is what sizes each pixel and pads each scanline of an image at that depth.
func (s *Setup) FormatFor(depth uint8) (Format, bool) {
	for _, f := range s.Formats {
		if f.Depth == depth {
			return f, true
		}
	}
	return Format{}, false
}

// ScreenOf returns screen i, or nil when i names no screen.
func (s *Setup) ScreenOf(i int) *Screen {
	if i < 0 || i >= len(s.Screens) {
		return nil
	}
	return &s.Screens[i]
}

// ScreenOfRoot returns the index of the screen whose root window is root, and
// whether one matched.
func (s *Setup) ScreenOfRoot(root uint32) (int, bool) {
	for i := range s.Screens {
		if s.Screens[i].Root == root {
			return i, true
		}
	}
	return 0, false
}
