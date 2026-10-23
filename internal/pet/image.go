package pet

import _ "embed"

// defaultPNG is the bundled character: takina.png, as the Swift widget
// shipped it.
//
//go:embed assets/takina.png
var defaultPNG []byte
