package pet

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"  // decodeUpright
	_ "image/jpeg" // decodeUpright
	_ "image/png"  // decodeUpright
	"math"
	"slices"
	"time"

	_ "golang.org/x/image/bmp" // decodeUpright
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decodeUpright
)

const (
	// keyTolerance is how far a pixel may sit from the sampled background
	// and still belong to it.
	keyTolerance = 32.0

	// trimTolerance is how far from the background a pixel next to the
	// keyed area may sit and still count as the source's blend fringe.
	trimTolerance = 120.0

	// maxWorkSide caps the image the pipeline works on, so a photo costs
	// a bounded time and the pet window stays small.
	maxWorkSide = 512
)

// CutoutReport says what the cutout pipeline did. It is a value, so it
// cannot disagree with the bitmap it describes.
type CutoutReport struct {
	Background      color.NRGBA // sampled border color; zero when Transparent
	Transparent     bool        // the border was already transparent, so no color was keyed
	Keyed           bool        // a flat border color was found and removed
	RemovedFraction float64     // share of working pixels the key made transparent
	Rim             int         // white rim width in pixels, 0 when none was added
	SourceWidth     int
	SourceHeight    int
	WorkWidth       int
	WorkHeight      int
	Width           int
	Height          int
	Duration        time.Duration
}

// makeCutout keys src's flat background away and lays a white rim around
// what remains. It is a pure function of its input and never mutates src.
func makeCutout(src image.Image) (*image.NRGBA, CutoutReport) {
	start := time.Now()
	srcNRGBA := toNRGBA(src)
	report := CutoutReport{SourceWidth: srcNRGBA.Rect.Dx(), SourceHeight: srcNRGBA.Rect.Dy()}
	work := srcNRGBA
	if max(work.Rect.Dx(), work.Rect.Dy()) > maxWorkSide {
		work = downscale(work, maxWorkSide)
	}
	w, h := work.Rect.Dx(), work.Rect.Dy()
	report.WorkWidth, report.WorkHeight = w, h

	bg, transparent, flat := sampleBackground(work)
	report.Transparent = transparent
	report.Background = bg
	if flat {
		removed := keyBackground(work, bg)
		report.Keyed = removed > 0
		if report.Keyed {
			trimEdge(work, bg)
		}
		report.RemovedFraction = float64(removed) / float64(w*h)
	}
	if !hasAlpha(work) {
		// Nothing was removable: a white rim around a photo would draw a
		// rectangle border around it.
		report.Width, report.Height = w, h
		report.Duration = time.Since(start)
		return work, report
	}
	rim := min(max(int(math.Round(float64(min(w, h))*0.03)), 2), 64)
	out := outline(work, rim)
	report.Rim = rim
	report.Width, report.Height = out.Rect.Dx(), out.Rect.Dy()
	report.Duration = time.Since(start)
	return out, report
}

// sampleBackground reads the ring one pixel inside the border. A mostly
// transparent border means the picture needs no keying; otherwise the
// opaque pixels' per-channel median is the background candidate, and it is
// flat enough to key when enough of the ring sits close to it.
func sampleBackground(img *image.NRGBA) (bg color.NRGBA, transparent, flat bool) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w < 3 || h < 3 {
		return color.NRGBA{}, false, false
	}
	var rs, gs, bs []int
	total, transparentN := 0, 0
	sample := func(x, y int) {
		c := img.NRGBAAt(x, y)
		total++
		if c.A < 128 {
			transparentN++
			return
		}
		rs = append(rs, int(c.R))
		gs = append(gs, int(c.G))
		bs = append(bs, int(c.B))
	}
	// The border of the inset rectangle 1..w-2, 1..h-2; a side two pixels
	// long is walked once.
	for x := 1; x <= w-2; x++ {
		sample(x, 1)
		if h > 3 {
			sample(x, h-2)
		}
	}
	if h > 3 {
		for y := 2; y <= h-3; y++ {
			sample(1, y)
			if w > 3 {
				sample(w-2, y)
			}
		}
	}
	if transparentN*2 >= total {
		return color.NRGBA{}, true, false
	}
	med := color.NRGBA{uint8(median(rs)), uint8(median(gs)), uint8(median(bs)), 255}
	near := 0
	for i := range rs {
		if dist(color.NRGBA{uint8(rs[i]), uint8(gs[i]), uint8(bs[i]), 255}, med) <= keyTolerance {
			near++
		}
	}
	return med, false, float64(near)/float64(len(rs)) >= 0.30
}

// median is the middle value of xs.
func median(xs []int) int {
	slices.Sort(xs)
	return xs[len(xs)/2]
}

// keyBackground clears the background-colored pixels connected to the
// border, so an enclosed same-color region keeps its pixels, and returns
// how many pixels it cleared.
func keyBackground(img *image.NRGBA, bg color.NRGBA) int {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	seen := make([]bool, w*h)
	queue := make([]int32, 0, w+h)
	push := func(x, y int) {
		i := y*w + x
		if seen[i] {
			return
		}
		c := img.NRGBAAt(x, y)
		if c.A == 0 || dist(color.NRGBA{c.R, c.G, c.B, 255}, bg) > keyTolerance {
			return
		}
		seen[i] = true
		queue = append(queue, int32(i))
	}
	for x := 0; x < w; x++ {
		push(x, 0)
		push(x, h-1)
	}
	for y := 1; y < h-1; y++ {
		push(0, y)
		push(w-1, y)
	}
	removed := 0
	for len(queue) > 0 {
		i := int(queue[len(queue)-1])
		queue = queue[:len(queue)-1]
		x, y := i%w, i/w
		img.Pix[4*i+3] = 0
		removed++
		if x > 0 {
			push(x-1, y)
		}
		if x+1 < w {
			push(x+1, y)
		}
		if y > 0 {
			push(x, y-1)
		}
		if y+1 < h {
			push(x, y+1)
		}
	}
	return removed
}

// trimEdge drops one layer of pixels next to the keyed background that are
// still close to its color, where the source blended the two: without it
// the blend shows as a fringe around the white rim.
func trimEdge(img *image.NRGBA, bg color.NRGBA) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	clear := make([]bool, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			if img.Pix[4*i+3] == 0 {
				continue
			}
			edge := false
			for dy := -1; dy <= 1 && !edge; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nx, ny := x+dx, y+dy
					if nx < 0 || ny < 0 || nx >= w || ny >= h {
						continue
					}
					if img.Pix[4*(ny*w+nx)+3] == 0 {
						edge = true
						break
					}
				}
			}
			if !edge {
				continue
			}
			c := img.NRGBAAt(x, y)
			if dist(color.NRGBA{c.R, c.G, c.B, 255}, bg) <= trimTolerance {
				clear[i] = true
			}
		}
	}
	for i, c := range clear {
		if c {
			img.Pix[4*i+3] = 0
		}
	}
}

// dist is the straight RGB distance between two colors.
func dist(a, b color.NRGBA) float64 {
	dr := float64(a.R) - float64(b.R)
	dg := float64(a.G) - float64(b.G)
	db := float64(a.B) - float64(b.B)
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

// hasAlpha says whether any pixel is not fully opaque.
func hasAlpha(img *image.NRGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] < 255 {
			return true
		}
	}
	return false
}

// outline pads img by rim and draws a white rim under its alpha mask: the
// chamfer distance to the nearest opaque pixel fades the rim's outer edge,
// as a one-pixel antialiasing would.
func outline(img *image.NRGBA, rim int) *image.NRGBA {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	W, H := w+2*rim, h+2*rim
	dm := make([]float32, W*H)
	const far = 1e9
	for i := range dm {
		dm[i] = far
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if img.NRGBAAt(x, y).A > 8 {
				dm[(y+rim)*W+x+rim] = 0
			}
		}
	}
	const diag = 1.41421356
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			i := y*W + x
			d := dm[i]
			if x > 0 {
				d = min(d, dm[i-1]+1)
			}
			if y > 0 {
				d = min(d, dm[i-W]+1)
				if x > 0 {
					d = min(d, dm[i-W-1]+diag)
				}
				if x+1 < W {
					d = min(d, dm[i-W+1]+diag)
				}
			}
			dm[i] = d
		}
	}
	for y := H - 1; y >= 0; y-- {
		for x := W - 1; x >= 0; x-- {
			i := y*W + x
			d := dm[i]
			if x+1 < W {
				d = min(d, dm[i+1]+1)
			}
			if y+1 < H {
				d = min(d, dm[i+W]+1)
				if x > 0 {
					d = min(d, dm[i+W-1]+diag)
				}
				if x+1 < W {
					d = min(d, dm[i+W+1]+diag)
				}
			}
			dm[i] = d
		}
	}
	out := image.NewNRGBA(image.Rect(0, 0, W, H))
	for i, d := range dm {
		if a := float64(rim) + 0.5 - float64(d); a > 0 {
			out.Pix[4*i] = 255
			out.Pix[4*i+1] = 255
			out.Pix[4*i+2] = 255
			out.Pix[4*i+3] = uint8(min(a, 1) * 255)
		}
	}
	draw.Draw(out, image.Rect(rim, rim, rim+w, rim+h), img, image.Point{}, draw.Over)
	return out
}

// toNRGBA copies src into a straight-alpha NRGBA image at its bounds.
func toNRGBA(src image.Image) *image.NRGBA {
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Rect, src, b.Min, draw.Src)
	return out
}

// downscale shrinks img so its larger side is maxSide.
func downscale(img *image.NRGBA, maxSide int) *image.NRGBA {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	scale := float64(maxSide) / float64(max(w, h))
	W, H := max(1, int(math.Round(float64(w)*scale))), max(1, int(math.Round(float64(h)*scale)))
	out := image.NewNRGBA(image.Rect(0, 0, W, H))
	xdraw.ApproxBiLinear.Scale(out, out.Rect, img, img.Rect, xdraw.Src, nil)
	return out
}

// decodeUpright decodes a PNG, JPEG, GIF, WebP or BMP into an upright
// image. Mygo applies a JPEG's EXIF orientation when it makes a bitmap but
// does not expose decoded pixels, so the cutout path applies the same
// orientation here.
func decodeUpright(data []byte) (image.Image, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if format != "jpeg" {
		return img, nil
	}
	o := exifOrientation(data)
	if o <= 1 {
		return img, nil
	}
	return orient(toNRGBA(img), o), nil
}

// exifOrientation returns the orientation of a JPEG's EXIF data, 1 to 8,
// or 0 without one.
func exifOrientation(jpg []byte) int {
	if len(jpg) < 4 || jpg[0] != 0xff || jpg[1] != 0xd8 {
		return 0
	}
	for i := 2; i+4 <= len(jpg); {
		if jpg[i] != 0xff {
			return 0
		}
		marker, size := jpg[i+1], int(binary.BigEndian.Uint16(jpg[i+2:]))
		if marker == 0xda || size < 2 || i+2+size > len(jpg) { // the image data starts
			return 0
		}
		seg := jpg[i+4 : i+2+size]
		if marker == 0xe1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 0
}

// tiffOrientation reads the Orientation tag of the first IFD of TIFF data.
func tiffOrientation(t []byte) int {
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	ifd := int(bo.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 0
	}
	n := int(bo.Uint16(t[ifd:]))
	for e := ifd + 2; e+12 <= len(t) && n > 0; e, n = e+12, n-1 {
		if bo.Uint16(t[e:]) == 0x0112 && bo.Uint16(t[e+2:]) == 3 { // Orientation, a SHORT
			if o := int(bo.Uint16(t[e+8:])); o >= 1 && o <= 8 {
				return o
			}
			return 0
		}
	}
	return 0
}

// orient turns and flips img as EXIF orientation o says it is to show.
func orient(img *image.NRGBA, o int) *image.NRGBA {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	out := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			default:
				sx, sy = x, y
			}
			copy(out.Pix[4*(y*dw+x):4*(y*dw+x)+4], img.Pix[4*(sy*w+sx):])
		}
	}
	return out
}
