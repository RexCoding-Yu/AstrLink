package imagecompress

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"

	xwebp "golang.org/x/image/webp"
)

// screenshot draws flat panels with text-like strokes, the kind of image an
// agent sends most, and a transparent corner whose hidden color must survive
// a lossless encode.
func screenshot(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.NRGBA{246, 247, 249, 255}}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, width/5, height), &image.Uniform{color.NRGBA{233, 236, 241, 255}}, image.Point{}, draw.Src)
	random := rand.New(rand.NewSource(7))
	for y := 12; y+10 < height; y += 18 {
		for x := width/5 + 16; x < width-40; x += 6 + random.Intn(40) {
			run := 4 + random.Intn(30)
			ink := color.NRGBA{uint8(30 + random.Intn(40)), uint8(40 + random.Intn(40)), uint8(60 + random.Intn(60)), 255}
			draw.Draw(img, image.Rect(x, y, min(x+run, width), y+2+random.Intn(8)), &image.Uniform{ink}, image.Point{}, draw.Src)
		}
	}
	for y := range 8 {
		for x := range 8 {
			img.SetNRGBA(x, y, color.NRGBA{200, 10, 90, 0})
		}
	}
	return img
}

// photo is smooth color with sensor-like noise, which lossy codecs keep
// small and lossless codecs cannot.
func photo(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	random := rand.New(rand.NewSource(3))
	for y := range height {
		for x := range width {
			noise := random.Intn(24) - 12
			img.SetNRGBA(x, y, color.NRGBA{
				clampByte(80 + x*120/width + noise), clampByte(120 + y*80/height + noise), clampByte(160 - x*60/width + noise), 255,
			})
		}
	}
	return img
}

func clampByte(value int) uint8 {
	return uint8(min(max(value, 0), 255))
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image, quality int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func nrgba(img image.Image) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out
}

var lossless = Options{}

func TestLosslessKeepsEveryPixelAndShrinksScreenshots(t *testing.T) {
	source := screenshot(1280, 800)
	data := encodePNG(t, source)
	encoded, ok := new(Compressor).WebP(context.Background(), data, lossless)
	if !ok || len(encoded) >= len(data) {
		t.Fatalf("lossless WebP = %v, %d bytes from %d", ok, len(encoded), len(data))
	}
	decoded, err := xwebp.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(nrgba(decoded).Pix, source.Pix) {
		t.Fatal("lossless WebP changed pixels, including those under transparency")
	}
	// A flat screenshot is smaller lossless than lossy, so lossy mode sends
	// the exact image.
	if lossy, ok := new(Compressor).WebP(context.Background(), data, Options{Lossy: true, Quality: 80}); !ok || !bytes.Equal(lossy, encoded) {
		t.Fatalf("lossy mode sent %d bytes instead of the %d-byte lossless WebP", len(lossy), len(encoded))
	}
}

func TestLosslessTakesSingleFrameGIFsOnly(t *testing.T) {
	palette := color.Palette{color.White, color.Black, color.NRGBA{200, 30, 30, 255}, color.Transparent}
	frame := image.NewPaletted(image.Rect(0, 0, 400, 300), palette)
	draw.Draw(frame, frame.Bounds(), screenshot(400, 300), image.Point{}, draw.Src)
	var single bytes.Buffer
	if err := gif.EncodeAll(&single, &gif.GIF{Image: []*image.Paletted{frame}, Delay: []int{0}}); err != nil {
		t.Fatal(err)
	}
	encoded, ok := new(Compressor).WebP(context.Background(), single.Bytes(), lossless)
	if !ok {
		t.Fatal("single-frame GIF was not re-encoded")
	}
	decoded, err := xwebp.Decode(bytes.NewReader(encoded))
	if err != nil || !bytes.Equal(nrgba(decoded).Pix, nrgba(frame).Pix) {
		t.Fatalf("GIF pixels changed: %v", err)
	}
	var animated bytes.Buffer
	if err := gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := new(Compressor).WebP(context.Background(), animated.Bytes(), Options{Lossy: true, Quality: 80}); ok {
		t.Fatal("animated GIF was flattened to one frame")
	}
}

func TestModesChooseWhichSourcesChange(t *testing.T) {
	compressor := new(Compressor)
	jpegData := encodeJPEG(t, photo(1024, 768), 95)
	if _, ok := compressor.WebP(context.Background(), jpegData, lossless); ok {
		t.Fatal("lossless mode re-encoded a JPEG")
	}
	lossy, ok := compressor.WebP(context.Background(), jpegData, Options{Lossy: true, Quality: 80})
	if !ok || len(lossy) >= len(jpegData) {
		t.Fatalf("lossy JPEG = %v, %d bytes from %d", ok, len(lossy), len(jpegData))
	}
	if _, err := xwebp.Decode(bytes.NewReader(lossy)); err != nil {
		t.Fatal(err)
	}
	// A WebP no smaller than its source is not used.
	small := encodeJPEG(t, photo(1024, 768), 20)
	if _, ok := compressor.WebP(context.Background(), small, Options{Lossy: true, Quality: 100}); ok {
		t.Fatal("a larger WebP replaced a small JPEG")
	}

	deep := image.NewNRGBA64(image.Rect(0, 0, 320, 240))
	draw.Draw(deep, deep.Bounds(), screenshot(320, 240), image.Point{}, draw.Src)
	deepPNG := encodePNG(t, deep)
	if _, ok := compressor.WebP(context.Background(), deepPNG, lossless); ok {
		t.Fatal("lossless mode dropped a 16-bit depth")
	}
	if _, ok := compressor.WebP(context.Background(), deepPNG, Options{Lossy: true, Quality: 85}); !ok {
		t.Fatal("lossy mode kept a 16-bit PNG")
	}

	for name, data := range map[string][]byte{
		"tiny":    encodePNG(t, screenshot(16, 16)),
		"webp":    lossy,
		"garbage": bytes.Repeat([]byte("not an image "), 1024),
	} {
		if _, ok := compressor.WebP(context.Background(), data, Options{Lossy: true, Quality: 85}); ok {
			t.Fatalf("%s input was re-encoded", name)
		}
	}
}

// withOrientation inserts an EXIF block with one orientation entry after the
// JPEG's start marker.
func withOrientation(data []byte, orientation uint16) []byte {
	tiff := []byte("MM\x00\x2a\x00\x00\x00\x08\x00\x01")
	tiff = binary.BigEndian.AppendUint16(tiff, 0x0112)
	tiff = binary.BigEndian.AppendUint16(tiff, 3)
	tiff = binary.BigEndian.AppendUint32(tiff, 1)
	tiff = binary.BigEndian.AppendUint16(tiff, orientation)
	tiff = append(tiff, 0, 0, 0, 0, 0, 0)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := binary.BigEndian.AppendUint16([]byte{0xff, 0xe1}, uint16(len(payload)+2))
	segment = append(segment, payload...)
	return append(append(append([]byte(nil), data[:2]...), segment...), data[2:]...)
}

func TestLossyKeepsRotatedPhotos(t *testing.T) {
	data := encodeJPEG(t, photo(800, 600), 95)
	options := Options{Lossy: true, Quality: 80}
	if _, ok := new(Compressor).WebP(context.Background(), withOrientation(data, 6), options); ok {
		t.Fatal("a photo rotated by EXIF lost its rotation")
	}
	if _, ok := new(Compressor).WebP(context.Background(), withOrientation(data, 1), options); !ok {
		t.Fatal("an upright EXIF photo was kept")
	}
}

func TestResultsAreStableAndCached(t *testing.T) {
	data := encodePNG(t, screenshot(960, 600))
	first, ok := new(Compressor).WebP(context.Background(), data, lossless)
	if !ok {
		t.Fatal("not re-encoded")
	}
	compressor := new(Compressor)
	second, _ := compressor.WebP(context.Background(), data, lossless)
	if !bytes.Equal(first, second) {
		t.Fatal("two encoders gave different bytes; upstream prompt caches would miss")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if cached, ok := compressor.WebP(cancelled, data, lossless); !ok || !bytes.Equal(cached, first) {
		t.Fatal("a cached image was not served")
	}
	if _, ok := compressor.WebP(cancelled, encodePNG(t, screenshot(900, 500)), lossless); ok {
		t.Fatal("a done context still encoded")
	}
	// Results are kept per mode and quality.
	noisy := encodePNG(t, photo(640, 480))
	exact, _ := compressor.WebP(context.Background(), noisy, lossless)
	low, _ := compressor.WebP(context.Background(), noisy, Options{Lossy: true, Quality: 30})
	high, _ := compressor.WebP(context.Background(), noisy, Options{Lossy: true, Quality: 95})
	if len(exact) == 0 || len(low) == 0 || len(low) >= len(high) || len(high) >= len(exact) {
		t.Fatalf("lossless %d, q30 %d, q95 %d bytes", len(exact), len(low), len(high))
	}
}

func TestCacheEvictsOldestBeyondItsBudget(t *testing.T) {
	compressor := new(Compressor)
	compressor.init()
	big := make([]byte, maxCacheBytes/4-cacheEntryBytes)
	keys := make([]cacheKey, 5)
	for index := range keys {
		keys[index].sum[0] = byte(index + 1)
		compressor.store(keys[index], big)
	}
	if _, found := compressor.lookup(keys[0]); found {
		t.Fatal("oldest entry survived")
	}
	for _, key := range keys[1:] {
		if _, found := compressor.lookup(key); !found {
			t.Fatal("recent entry evicted")
		}
	}
	if compressor.bytes > maxCacheBytes {
		t.Fatalf("cache holds %d bytes", compressor.bytes)
	}
}
