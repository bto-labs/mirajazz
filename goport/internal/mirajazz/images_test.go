package mirajazz

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func solidImage(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestConvertJPEGRoundTrips(t *testing.T) {
	src := solidImage(20, 10, color.RGBA{200, 100, 50, 255})
	format := ImageFormat{Mode: ImageModeJPEG, Width: 60, Height: 60}
	data, err := ConvertImageWithFormat(format, src)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("output is not valid JPEG: %v", err)
	}
	if got := img.Bounds(); got.Dx() != 60 || got.Dy() != 60 {
		t.Errorf("decoded size = %v, want 60x60", got.Size())
	}
}

func TestConvertNoneIsEmpty(t *testing.T) {
	data, err := ConvertImageWithFormat(ImageFormat{Mode: ImageModeNone, Width: 10, Height: 10}, solidImage(4, 4, color.White))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("ImageModeNone should yield empty data, got %d bytes", len(data))
	}
}

func TestRotation90SwapsDimensions(t *testing.T) {
	src := solidImage(4, 2, color.White)
	rotated := applyRotation(src, Rot90)
	if rotated.Bounds().Dx() != 2 || rotated.Bounds().Dy() != 4 {
		t.Errorf("Rot90 dims = %v, want 2x4", rotated.Bounds().Size())
	}
}

func TestRotation90IsClockwise(t *testing.T) {
	// Top-left pixel red, rest black; after a clockwise 90 rotation the red
	// pixel must land at the top-right.
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.RGBA{255, 0, 0, 255})
	rotated := applyRotation(src, Rot90)
	r, _, _, _ := rotated.At(1, 0).RGBA()
	if byte(r>>8) != 255 {
		t.Errorf("clockwise Rot90 did not move top-left to top-right; got r=%d", byte(r>>8))
	}
}

func TestMirrorXFlipsHorizontally(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, color.RGBA{255, 0, 0, 255})
	mirrored := applyMirror(src, MirrorX)
	r, _, _, _ := mirrored.At(1, 0).RGBA()
	if byte(r>>8) != 255 {
		t.Errorf("MirrorX did not move left pixel to the right")
	}
}

func TestEncodeBMPHeader(t *testing.T) {
	data := encodeBMP(solidImage(2, 2, color.RGBA{10, 20, 30, 255}))
	if data[0] != 'B' || data[1] != 'M' {
		t.Fatalf("missing BM signature")
	}
	// 2x2 24-bit: rowSize = (2*3+3)&^3 = 8, imageSize = 16, file = 54+16 = 70.
	if len(data) != 70 {
		t.Errorf("BMP size = %d, want 70", len(data))
	}
	// Pixel data is BGR: first stored pixel (bottom row) should be 30,20,10.
	px := data[54:57]
	if px[0] != 30 || px[1] != 20 || px[2] != 10 {
		t.Errorf("BGR pixel = % d, want [30 20 10]", px)
	}
}

func TestImageRectFromImage(t *testing.T) {
	rect, err := ImageRectFromImage(solidImage(8, 6, color.White))
	if err != nil {
		t.Fatal(err)
	}
	if rect.W != 8 || rect.H != 6 {
		t.Errorf("rect dims = %dx%d, want 8x6", rect.W, rect.H)
	}
	if _, err := jpeg.Decode(bytes.NewReader(rect.Data)); err != nil {
		t.Errorf("rect data not valid JPEG: %v", err)
	}
}
