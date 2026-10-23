package pet

import (
	"bytes"
	"image/png"
	"os"

	"github.com/egoist/mygo/ui"
)

// Character is the pet's character image: the decoded file plus the cutout
// version made on first use. The settings flag picks which one shows. It is
// touched on the main thread only.
type Character struct {
	data []byte
	raw  *ui.Bitmap
	// cut stays once made, so turning the checkbox off and on again shows
	// it without processing the image twice; on picks which bitmap shows.
	cut    *ui.Bitmap
	on     bool
	report CutoutReport
}

// DefaultCharacter loads the bundled character image.
func DefaultCharacter(cutout bool) (*Character, error) {
	return NewCharacterBytes(defaultPNG, cutout)
}

// LoadCharacter loads the character image at path, or the bundled one when
// path is empty.
func LoadCharacter(path string, cutout bool) (*Character, error) {
	if path == "" {
		return DefaultCharacter(cutout)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return NewCharacterBytes(data, cutout)
}

// NewCharacterBytes decodes a PNG, JPEG, GIF, WebP or BMP as the character.
func NewCharacterBytes(data []byte, cutout bool) (*Character, error) {
	raw, err := ui.DecodeBitmap(data)
	if err != nil {
		return nil, err
	}
	c := &Character{data: data, raw: raw}
	if cutout {
		if err := c.SetCutout(true); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Bitmap returns the bitmap to show: the cutout one while it is on.
func (c *Character) Bitmap() *ui.Bitmap {
	if c.on && c.cut != nil {
		return c.cut
	}
	return c.raw
}

// SetCutout shows the cutout bitmap when on, making it from the file the
// first time. A later on reuses it.
func (c *Character) SetCutout(on bool) error {
	if !on {
		c.on = false
		return nil
	}
	if c.cut == nil {
		img, err := decodeUpright(c.data)
		if err != nil {
			return err
		}
		cut, report := makeCutout(img)
		c.cut = ui.NewBitmap(cut)
		c.report = report
	}
	c.on = true
	return nil
}

// Report says what the cutout pipeline did the last time it ran.
func (c *Character) Report() CutoutReport { return c.report }

// Capture returns a PNG of the active pixels, re-derived from the file so
// no third image stays resident, for the E2E harness.
func (c *Character) Capture() ([]byte, error) {
	img, err := decodeUpright(c.data)
	if err != nil {
		return nil, err
	}
	if c.on && c.cut != nil {
		img, _ = makeCutout(img)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
