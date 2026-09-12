package tui

import (
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"os"
	"strings"

	"golang.org/x/image/draw"
)

type renderedPreview struct {
	data string
}

func renderPlaceholder(imagePath string, cols, rows int) (renderedPreview, error) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}

	f, err := os.Open(imagePath)
	if err != nil {
		return renderedPreview{}, err
	}
	defer func() {
		_ = f.Close()
	}()

	img, _, err := image.Decode(f)
	if err != nil {
		return renderedPreview{}, err
	}

	targetW := cols
	targetH := rows * 2 // 2 pixels per terminal character (upper/lower half blocks)

	// Create the canvas and fill it with your terminal background color (Letterboxing)
	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{color.RGBA{17, 17, 27, 255}}, image.Point{}, draw.Src)

	// Calculate aspect ratio to prevent stretching
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	ratio := float64(targetW) / float64(w)
	if rY := float64(targetH) / float64(h); rY < ratio {
		ratio = rY
	}

	drawW := int(float64(w) * ratio)
	drawH := int(float64(h) * ratio)
	if drawW > 0 && drawH > 0 {
		offsetX := (targetW - drawW) / 2
		offsetY := (targetH - drawH) / 2
		drawRect := image.Rect(offsetX, offsetY, offsetX+drawW, offsetY+drawH)

		// High-quality Catmull-Rom resampling
		draw.CatmullRom.Scale(dst, drawRect, img, img.Bounds(), draw.Over, nil)
	}

	var sb strings.Builder
	for y := 0; y < targetH; y += 2 {
		for x := 0; x < targetW; x++ {
			top := dst.RGBAAt(x, y)
			bottom := dst.RGBAAt(x, y+1)

			fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀",
				top.R, top.G, top.B,
				bottom.R, bottom.G, bottom.B)
		}
		sb.WriteString("\x1b[0m\n")
	}

	return renderedPreview{data: strings.TrimSpace(sb.String())}, nil
}
