package render

import (
	"fmt"
	"os"
	"path/filepath"
)

// family is the face used for every glyph. Noto Sans CJK
// ships it in two weights, so the layouts use two.
const family = "Noto Sans CJK SC"

var fontDirs = []string{
	"/usr/share/fonts/opentype/noto",
	"/usr/share/fonts/truetype/noto",
	"/usr/share/fonts/noto-cjk",
	"/usr/share/fonts/noto",
}

// fontFile returns the Noto Sans CJK collection holding weight.
func fontFile(w Weight) (string, error) {
	name := "NotoSansCJK-Regular.ttc"
	if w == Bold {
		name = "NotoSansCJK-Bold.ttc"
	}
	for _, dir := range fontDirs {
		file := filepath.Join(dir, name)
		if info, err := os.Stat(file); err == nil && info.Mode().IsRegular() {
			return file, nil
		}
	}
	return "", fmt.Errorf("render: %s not found; install the Noto Sans CJK fonts", name)
}
