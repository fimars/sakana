package pet

import (
	_ "embed"
	"os"

	"github.com/egoist/mygo/ui"
)

// defaultPNG is the bundled character: takina.png, as the Swift widget
// shipped it.
//
//go:embed assets/takina.png
var defaultPNG []byte

// DefaultBitmap decodes the bundled character image.
func DefaultBitmap() (*ui.Bitmap, error) {
	return ui.DecodeBitmap(defaultPNG)
}

// LoadBitmap decodes the character image at path, or the bundled one when
// path is empty.
func LoadBitmap(path string) (*ui.Bitmap, error) {
	if path == "" {
		return DefaultBitmap()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadBitmapBytes(data)
}

// LoadBitmapBytes decodes a PNG or JPEG image the user chose.
func LoadBitmapBytes(data []byte) (*ui.Bitmap, error) {
	return ui.DecodeBitmap(data)
}

// DefaultBitmapBytes returns the bundled character's PNG bytes.
func DefaultBitmapBytes() []byte { return defaultPNG }
