package pet

import "math"

// Metrics is the viewport a character of a given size needs: the window
// MyGo draws it in, and the parts of the rod and the base as the widget
// lays them out.
type Metrics struct {
	Width, Height float64
	ImageSize     float64
	RodLength     float64
	BaseWidth     float64
	BaseHeight    float64
	BasePad       float64
	// RodVerticalScale scales how far the rod bends down when the
	// character is pulled: 0.6, as the Swift widget had it.
	RodVerticalScale float64
	MaxR             float64
	MaxY             float64
}

// BaseTopY is the y of the point the rod hangs from, in window DIPs.
func (m Metrics) BaseTopY() float64 { return m.Height - m.BaseHeight - m.BasePad }

// MidX is the x the base is centered on, in window DIPs.
func (m Metrics) MidX() float64 { return m.Width / 2 }

// PreferredImageSize is how large a bitmap shows: its smaller side, up to
// 120 DIPs, as calculateViewport(for:) did.
func PreferredImageSize(w, h int) float64 {
	side := math.Min(float64(w), float64(h))
	return math.Min(side, 120)
}

// MetricsFor computes the window a character imageSize DIPs large needs,
// porting calculateViewport(imageSize:): the box that holds the character
// at its largest rotation, the rod, and the base, with a margin.
func MetricsFor(imageSize float64) Metrics {
	const (
		baseHeight       = 6.0
		basePad          = 4.0
		safetyMargin     = 20.0
		rodVerticalScale = 0.6
	)
	maxR := math.Min(math.Max(imageSize/5, 30), 60)
	maxY := imageSize / 4
	rodLength := imageSize * 0.5625
	rad := maxR * math.Pi / 180
	halfWidth := math.Sin(rad)*rodLength + (imageSize/2)*math.Cos(rad) + imageSize*math.Sin(rad)
	width := 2*halfWidth + safetyMargin
	height := basePad + baseHeight + rodLength + maxY*rodVerticalScale + imageSize + safetyMargin
	return Metrics{
		Width:            width,
		Height:           height,
		ImageSize:        imageSize,
		RodLength:        rodLength,
		BaseWidth:        imageSize * 0.625,
		BaseHeight:       baseHeight,
		BasePad:          basePad,
		RodVerticalScale: rodVerticalScale,
		MaxR:             maxR,
		MaxY:             maxY,
	}
}
