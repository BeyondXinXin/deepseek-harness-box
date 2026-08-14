// Command makeicon generates the multi-resolution application icon used by HarnessBox.
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

var iconSizes = []int{16, 24, 32, 48, 64, 128, 256}

type icoEntry struct {
	size int
	data []byte
}

func main() {
	entries := make([]icoEntry, 0, len(iconSizes))
	for _, size := range iconSizes {
		icon := render(size)
		data, err := encodePNG(icon)
		if err != nil {
			log.Fatal(err)
		}
		entries = append(entries, icoEntry{size: size, data: data})
		if size == 256 {
			mustWritePNG(filepath.Join("assets", "harnessbox.png"), icon)
		}
	}
	if err := os.MkdirAll("assets", 0755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("assets", "harnessbox.ico"), encodeICO(entries), 0644); err != nil {
		log.Fatal(err)
	}
}

func render(size int) image.Image {
	const scale = 4
	canvas := image.NewNRGBA(image.Rect(0, 0, size*scale, size*scale))
	s := float64(size * scale)
	for y := 0; y < int(s); y++ {
		for x := 0; x < int(s); x++ {
			xf, yf := float64(x)+0.5, float64(y)+0.5
			if roundedRect(xf, yf, .06*s, .06*s, .88*s, .88*s, .20*s) {
				t := yf / s
				canvas.SetNRGBA(x, y, color.NRGBA{R: uint8(43 + 18*t), G: uint8(59 + 46*t), B: uint8(143 + 67*t), A: 255})
			}
		}
	}

	// Box outline: a compact runtime container, recognizable even at tray-icon size.
	paint(canvas, color.NRGBA{R: 244, G: 248, B: 255, A: 255}, func(x, y float64) bool {
		return roundedRect(x, y, .22*s, .29*s, .56*s, .43*s, .065*s) ||
			roundedRect(x, y, .35*s, .21*s, .30*s, .12*s, .035*s)
	})
	paint(canvas, color.NRGBA{R: 54, G: 88, B: 185, A: 255}, func(x, y float64) bool {
		return roundedRect(x, y, .255*s, .325*s, .49*s, .36*s, .038*s)
	})

	// The H-shaped harness has two terminals and a bright bridge to signal a bundled launcher.
	paint(canvas, color.NRGBA{R: 103, G: 232, B: 255, A: 255}, func(x, y float64) bool {
		return roundedRect(x, y, .335*s, .405*s, .085*s, .20*s, .042*s) ||
			roundedRect(x, y, .58*s, .405*s, .085*s, .20*s, .042*s) ||
			roundedRect(x, y, .39*s, .47*s, .22*s, .07*s, .035*s)
	})
	paint(canvas, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, func(x, y float64) bool {
		return circle(x, y, .3775*s, .375*s, .032*s) || circle(x, y, .6225*s, .375*s, .032*s)
	})
	return downsample(canvas, size)
}

func paint(img *image.NRGBA, fill color.NRGBA, contains func(float64, float64) bool) {
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if contains(float64(x)+.5, float64(y)+.5) {
				img.SetNRGBA(x, y, fill)
			}
		}
	}
}

func roundedRect(x, y, left, top, width, height, radius float64) bool {
	right, bottom := left+width, top+height
	if x < left || x > right || y < top || y > bottom {
		return false
	}
	cx := math.Max(left+radius, math.Min(x, right-radius))
	cy := math.Max(top+radius, math.Min(y, bottom-radius))
	return math.Hypot(x-cx, y-cy) <= radius
}

func circle(x, y, cx, cy, radius float64) bool { return math.Hypot(x-cx, y-cy) <= radius }

func downsample(source *image.NRGBA, size int) *image.NRGBA {
	destination := image.NewNRGBA(image.Rect(0, 0, size, size))
	factor := source.Bounds().Dx() / size
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var red, green, blue, alpha uint32
			for yy := 0; yy < factor; yy++ {
				for xx := 0; xx < factor; xx++ {
					pixel := source.NRGBAAt(x*factor+xx, y*factor+yy)
					red, green, blue, alpha = red+uint32(pixel.R), green+uint32(pixel.G), blue+uint32(pixel.B), alpha+uint32(pixel.A)
				}
			}
			count := uint32(factor * factor)
			destination.SetNRGBA(x, y, color.NRGBA{R: uint8(red / count), G: uint8(green / count), B: uint8(blue / count), A: uint8(alpha / count)})
		}
	}
	return destination
}

func encodePNG(img image.Image) ([]byte, error) {
	var buffer bytes.Buffer
	err := png.Encode(&buffer, img)
	return buffer.Bytes(), err
}

func mustWritePNG(path string, img image.Image) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		log.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		log.Fatal(err)
	}
}

func encodeICO(entries []icoEntry) []byte {
	var buffer bytes.Buffer
	_ = binary.Write(&buffer, binary.LittleEndian, uint16(0))
	_ = binary.Write(&buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buffer, binary.LittleEndian, uint16(len(entries)))
	offset := 6 + len(entries)*16
	for _, entry := range entries {
		dimension := byte(entry.size)
		if entry.size == 256 {
			dimension = 0
		}
		buffer.WriteByte(dimension)
		buffer.WriteByte(dimension)
		buffer.WriteByte(0)
		buffer.WriteByte(0)
		_ = binary.Write(&buffer, binary.LittleEndian, uint16(1))
		_ = binary.Write(&buffer, binary.LittleEndian, uint16(32))
		_ = binary.Write(&buffer, binary.LittleEndian, uint32(len(entry.data)))
		_ = binary.Write(&buffer, binary.LittleEndian, uint32(offset))
		offset += len(entry.data)
	}
	for _, entry := range entries {
		buffer.Write(entry.data)
	}
	return buffer.Bytes()
}
