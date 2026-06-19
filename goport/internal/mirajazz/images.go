package mirajazz

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
)

// Image is the input type for image conversion. It aliases the standard
// library's image.Image so callers can decode with image/jpeg, image/png, etc.,
// without this package importing any third-party image crate (the Rust original
// uses the `image` crate).
type Image = image.Image

// jpegQuality matches the Rust crate's JpegEncoder::new_with_quality(.., 90).
const jpegQuality = 90

// ConvertImageWithFormat resizes, rotates, mirrors and encodes img according to
// format, returning the wire-ready bytes. It is the port of the Rust
// convert_image_with_format. ImageModeNone yields an empty slice.
func ConvertImageWithFormat(format ImageFormat, img Image) ([]byte, error) {
	// Nearest-neighbour resize to the exact target size (FilterType::Nearest).
	resized := resizeNearest(img, format.Width, format.Height)

	// Rotation, then mirroring (same order as the Rust crate).
	rotated := applyRotation(resized, format.Rotation)
	mirrored := applyMirror(rotated, format.Mirror)

	switch format.Mode {
	case ImageModeNone:
		return []byte{}, nil
	case ImageModeBMP:
		return encodeBMP(mirrored), nil
	case ImageModeJPEG:
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, mirrored, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return nil, ErrUnsupportedOperation
	}
}

// ImageRect is a JPEG-encoded image plus its dimensions, used for raw writes to
// an LCD screen. Port of the Rust ImageRect.
type ImageRect struct {
	W    uint16
	H    uint16
	Data []byte
}

// ImageRectFromImage JPEG-encodes img at its native size (quality 90).
func ImageRectFromImage(img Image) (*ImageRect, error) {
	b := img.Bounds()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return &ImageRect{
		W:    uint16(b.Dx()),
		H:    uint16(b.Dy()),
		Data: buf.Bytes(),
	}, nil
}

// resizeNearest scales src to w x h using nearest-neighbour sampling.
func resizeNearest(src Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == 0 || h == 0 {
		return dst
	}
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw == 0 || sh == 0 {
		return dst
	}
	for y := 0; y < h; y++ {
		sy := y * sh / h
		for x := 0; x < w; x++ {
			sx := x * sw / w
			dst.Set(x, y, src.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

// applyRotation returns src rotated by the given amount. Rot90 is clockwise.
func applyRotation(src *image.RGBA, rot ImageRotation) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	switch rot {
	case Rot0:
		return src
	case Rot90: // clockwise: dst(x,y) = src(x' , y') ; new dims h x w
		dst := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(h-1-y, x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case Rot180:
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(w-1-x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case Rot270: // counter-clockwise; new dims h x w
		dst := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	default:
		return src
	}
}

// applyMirror returns src flipped per the given mode.
func applyMirror(src *image.RGBA, m ImageMirroring) *image.RGBA {
	if m == MirrorNone {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x, y
			if m == MirrorX || m == MirrorBoth {
				dx = w - 1 - x
			}
			if m == MirrorY || m == MirrorBoth {
				dy = h - 1 - y
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// encodeBMP writes a 24-bit, bottom-up, BI_RGB Windows bitmap (BGR pixel order,
// rows padded to a 4-byte boundary) — the same on-wire shape the Rust crate
// produces via the `image` crate's BmpEncoder for Rgb8 input.
func encodeBMP(img *image.RGBA) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	rowSize := (w*3 + 3) &^ 3 // pad each row to a multiple of 4 bytes
	imageSize := rowSize * h
	const fileHeaderSize = 14
	const infoHeaderSize = 40
	dataOffset := fileHeaderSize + infoHeaderSize
	fileSize := dataOffset + imageSize

	buf := bytes.NewBuffer(make([]byte, 0, fileSize))

	// BITMAPFILEHEADER
	buf.WriteByte('B')
	buf.WriteByte('M')
	writeU32(buf, uint32(fileSize))
	writeU32(buf, 0) // reserved
	writeU32(buf, uint32(dataOffset))

	// BITMAPINFOHEADER
	writeU32(buf, infoHeaderSize)
	writeU32(buf, uint32(w))
	writeU32(buf, uint32(h)) // positive height => bottom-up rows
	writeU16(buf, 1)         // planes
	writeU16(buf, 24)        // bits per pixel
	writeU32(buf, 0)         // BI_RGB, no compression
	writeU32(buf, uint32(imageSize))
	writeU32(buf, 2835) // ~72 DPI horizontal (pixels/meter)
	writeU32(buf, 2835) // ~72 DPI vertical
	writeU32(buf, 0)    // colors used
	writeU32(buf, 0)    // important colors

	// Pixel data: bottom row first, BGR, padded.
	pad := make([]byte, rowSize-w*3)
	row := make([]byte, w*3)
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			row[x*3+0] = byte(bl >> 8)
			row[x*3+1] = byte(g >> 8)
			row[x*3+2] = byte(r >> 8)
		}
		buf.Write(row)
		buf.Write(pad)
	}

	return buf.Bytes()
}

func writeU16(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}

func writeU32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}
