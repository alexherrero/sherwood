package web

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The app icon is drawn here, from one description, as an SVG and as PNGs in
// the sizes phones ask for. The test checks the committed files still match
// the description; to change the icon, edit the shapes and run
//
//	go test ./internal/web -run TestIconsMatchTheirSource -update-icons
var updateIcons = flag.Bool("update-icons", false, "rewrite the icon files in static/ from the description in icon_test.go")

// The icon is a pine tree, three stacked triangles on a short trunk, in pale
// green on deep green. Coordinates are in a 512-unit square. Everything sits
// inside the middle 80% circle, so Android can crop it to any mask shape.
const iconUnits = 512

var (
	iconBackground = color.RGBA{R: 0x14, G: 0x53, B: 0x2d, A: 0xff}
	iconTree       = color.RGBA{R: 0xdc, G: 0xfc, B: 0xe7, A: 0xff}

	iconTriangles = [][3][2]int64{
		{{256, 112}, {176, 224}, {336, 224}},
		{{256, 176}, {152, 304}, {360, 304}},
		{{256, 248}, {128, 384}, {384, 384}},
	}
	// The trunk: x from 232 to 280, y from 384 to 424.
	iconTrunk = [4]int64{232, 384, 280, 424}
)

// The PNG renders, by file name.
var iconPNGs = map[string]int{
	"apple-touch-icon.png": 180,
	"icon-192.png":         192,
	"icon-512.png":         512,
}

func iconHex(c color.RGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

func iconSVG() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 %d %d\">\n", iconUnits, iconUnits)
	fmt.Fprintf(&b, "<rect width=\"%d\" height=\"%d\" fill=\"%s\"/>\n", iconUnits, iconUnits, iconHex(iconBackground))
	fmt.Fprintf(&b, "<g fill=\"%s\">\n", iconHex(iconTree))
	for _, t := range iconTriangles {
		fmt.Fprintf(&b, "<polygon points=\"%d,%d %d,%d %d,%d\"/>\n", t[0][0], t[0][1], t[1][0], t[1][1], t[2][0], t[2][1])
	}
	x0, y0, x1, y1 := iconTrunk[0], iconTrunk[1], iconTrunk[2], iconTrunk[3]
	fmt.Fprintf(&b, "<rect x=\"%d\" y=\"%d\" width=\"%d\" height=\"%d\"/>\n", x0, y0, x1-x0, y1-y0)
	b.WriteString("</g>\n</svg>\n")
	return []byte(b.String())
}

// iconSamples is the anti-aliasing grid: each pixel is sampled at
// iconSamples × iconSamples points.
const iconSamples = 4

// renderIcon draws the icon at size × size pixels. It uses whole numbers
// only, so every platform draws the same pixels.
func renderIcon(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	// A sample point (px*S+sx+½)/(size*S) of the way across is compared
	// with a shape point u/512 of the way across. Multiplying both by
	// 2*size*S*512 makes them whole numbers on the same scale: a sample
	// becomes (2*(px*S+sx)+1)*512 and a shape point u*2*size*S.
	shapeScale := int64(2 * size * iconSamples)
	inside := func(x, y int64) bool {
		for _, t := range iconTriangles {
			if inTriangle(x, y, t, shapeScale) {
				return true
			}
		}
		return x >= iconTrunk[0]*shapeScale && x <= iconTrunk[2]*shapeScale &&
			y >= iconTrunk[1]*shapeScale && y <= iconTrunk[3]*shapeScale
	}
	total := iconSamples * iconSamples
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			hits := 0
			for sy := 0; sy < iconSamples; sy++ {
				for sx := 0; sx < iconSamples; sx++ {
					x := int64(2*(px*iconSamples+sx)+1) * iconUnits
					y := int64(2*(py*iconSamples+sy)+1) * iconUnits
					if inside(x, y) {
						hits++
					}
				}
			}
			img.SetRGBA(px, py, blend(iconBackground, iconTree, hits, total))
		}
	}
	return img
}

func inTriangle(x, y int64, t [3][2]int64, scale int64) bool {
	edge := func(a, b [2]int64) int64 {
		ax, ay, bx, by := a[0]*scale, a[1]*scale, b[0]*scale, b[1]*scale
		return (bx-ax)*(y-ay) - (by-ay)*(x-ax)
	}
	e0, e1, e2 := edge(t[0], t[1]), edge(t[1], t[2]), edge(t[2], t[0])
	return (e0 >= 0 && e1 >= 0 && e2 >= 0) || (e0 <= 0 && e1 <= 0 && e2 <= 0)
}

func blend(bg, fg color.RGBA, k, n int) color.RGBA {
	mix := func(a, b uint8) uint8 { return uint8((int(a)*(n-k) + int(b)*k + n/2) / n) } //nolint:gosec // G115: a weighted mean of two bytes is a byte
	return color.RGBA{R: mix(bg.R, fg.R), G: mix(bg.G, fg.G), B: mix(bg.B, fg.B), A: 0xff}
}

func TestIconsMatchTheirSource(t *testing.T) {
	svgPath := filepath.Join("static", "icon.svg")
	if *updateIcons {
		if err := os.WriteFile(svgPath, iconSVG(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(svgPath) //nolint:gosec // G304: a fixed path in this package
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, iconSVG()) {
		t.Errorf("%s does not match the icon description; rerun with -update-icons", svgPath)
	}

	for name, size := range iconPNGs {
		p := filepath.Join("static", name)
		want := renderIcon(size)
		if *updateIcons {
			var buf bytes.Buffer
			if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, want); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		f, err := os.Open(p) //nolint:gosec // G304: a fixed name in this package
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Errorf("%s is %dx%d, want %dx%d", p, b.Dx(), b.Dy(), size, size)
			continue
		}
		if !samePixels(img, want) {
			t.Errorf("%s does not match the icon description; rerun with -update-icons", p)
		}
	}
}

func samePixels(a image.Image, b *image.RGBA) bool {
	r := b.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			ar, ag, ab, aa := a.At(x, y).RGBA()
			br, bg, bb, ba := b.At(x, y).RGBA()
			if ar != br || ag != bg || ab != bb || aa != ba {
				return false
			}
		}
	}
	return true
}

// The tree must stay inside the maskable safe zone: the circle of radius 40%
// of the icon about its center.
func TestTheIconFitsTheMaskableSafeZone(t *testing.T) {
	const c, r = iconUnits / 2, iconUnits * 2 / 5
	check := func(x, y int64) {
		if (x-c)*(x-c)+(y-c)*(y-c) > r*r {
			t.Errorf("the point (%d,%d) is outside the maskable safe zone", x, y)
		}
	}
	for _, tri := range iconTriangles {
		for _, p := range tri {
			check(p[0], p[1])
		}
	}
	x0, y0, x1, y1 := iconTrunk[0], iconTrunk[1], iconTrunk[2], iconTrunk[3]
	check(x0, y0)
	check(x1, y0)
	check(x0, y1)
	check(x1, y1)
}
