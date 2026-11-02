package app

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSolidPNG encodes one solid-colour PNG so the viewer tests decode a real
// image and can sample its painted pixels.
func writeSolidPNG(t *testing.T, path string, width, height int, fill color.Color) {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: fill}, image.Point{}, draw.Src)

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := png.Encode(file, img); err != nil {
		_ = file.Close()

		t.Fatal(err)
	}

	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// newImageTestApp builds a disk workspace with README.md plus one image and
// opens the image in the first editor group.
func newImageTestApp(t *testing.T, imagePath string, width, height int, fill color.Color) (*App, string) {
	t.Helper()

	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Workspace\n")
	writeSolidPNG(t, filepath.Join(dir, imagePath), width, height, fill)

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Cleanup(app.Close)

	app.openFile(imagePath)

	return app, dir
}

// TestImageViewRendersPNG is the key end-to-end proof that an opened PNG
// paints: the view carries the decoded size and the page samples red at the
// canvas centre after a real redraw.
func TestImageViewRendersPNG(t *testing.T) {
	app, _ := newImageTestApp(t, "red.png", 80, 40, color.RGBA{R: 255, A: 255})

	view := app.View()
	if !view.Image.On {
		t.Fatal("image view is off for an opened PNG")
	}

	if view.Image.Src == "" {
		t.Fatal("image view has no source")
	}

	if view.Image.NaturalW != 80 || view.Image.NaturalH != 40 {
		t.Fatalf("natural size = %dx%d, want 80x40", view.Image.NaturalW, view.Image.NaturalH)
	}

	if view.CanEdit {
		t.Fatal("image document is editable")
	}

	if view.Previewable {
		t.Fatal("image document is previewable")
	}

	if view.Lang != langImage {
		t.Fatalf("lang = %q, want %q", view.Lang, langImage)
	}

	if view.Editing || app.editing {
		t.Fatal("image document entered edit mode")
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	body, ok := boxByID(app.Page().Boxes(), "code-body")
	if !ok {
		t.Fatal("missing code-body box")
	}

	if _, ok := boxByID(app.Page().Boxes(), "image-view"); !ok {
		t.Fatal("missing image-view box")
	}

	canvas, ok := boxByID(app.Page().Boxes(), "image-canvas")
	if !ok {
		t.Fatal("missing image-canvas box")
	}

	// The 1 px border can widen the box, so allow a small ratio tolerance.
	if ratio := canvas.W / canvas.H; math.Abs(ratio-2) > 0.1 {
		t.Fatalf("canvas box %.0fx%.0f has ratio %.3f, want 2:1", canvas.W, canvas.H, ratio)
	}

	if canvas.X < body.X || canvas.Y < body.Y ||
		canvas.X+canvas.W > body.X+body.W || canvas.Y+canvas.H > body.Y+body.H {
		t.Fatalf("canvas %.0f,%.0f %.0fx%.0f escapes editor body %.0f,%.0f %.0fx%.0f",
			canvas.X, canvas.Y, canvas.W, canvas.H, body.X, body.Y, body.W, body.H)
	}

	img := mustDecodePNG(t, app.Page().Probe().PNG())
	cx := int(canvas.X + canvas.W/2)
	cy := int(canvas.Y + canvas.H/2)
	r, g, b, _ := img.At(cx, cy).RGBA()

	if r>>8 < 200 || g>>8 > 60 || b>>8 > 60 {
		t.Fatalf("canvas centre (%d,%d) = rgb(%d,%d,%d), want red", cx, cy, r>>8, g>>8, b>>8)
	}
}

// TestImageViewFitsLargeImage checks a large image is scaled down to the pane
// without stretching its aspect ratio or upscaling it.
func TestImageViewFitsLargeImage(t *testing.T) {
	app, _ := newImageTestApp(t, "large.png", 1600, 800, color.RGBA{G: 200, A: 255})

	view := app.View()
	if !view.Image.On {
		t.Fatal("image view is off for the large PNG")
	}

	if view.Image.W >= view.Image.NaturalW || view.Image.H >= view.Image.NaturalH {
		t.Fatalf("display %dx%d did not shrink natural %dx%d",
			view.Image.W, view.Image.H, view.Image.NaturalW, view.Image.NaturalH)
	}

	wantH := float64(view.Image.W) * float64(view.Image.NaturalH) / float64(view.Image.NaturalW)
	if math.Abs(float64(view.Image.H)-wantH) > 1 {
		t.Fatalf("display %dx%d does not preserve 2:1 ratio", view.Image.W, view.Image.H)
	}

	if view.Image.Scale >= zoomPercent {
		t.Fatalf("scale = %d%%, want below %d%%", view.Image.Scale, zoomPercent)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	body, ok := boxByID(app.Page().Boxes(), "code-body")
	if !ok {
		t.Fatal("missing code-body box")
	}

	if view.Image.W > int(body.W) || view.Image.H > int(body.H) {
		t.Fatalf("display %dx%d exceeds pane %.0fx%.0f",
			view.Image.W, view.Image.H, body.W, body.H)
	}
}

// TestImageViewSplitPane checks the right editor group renders its own image
// canvas when both panes hold the same PNG.
func TestImageViewSplitPane(t *testing.T) {
	app, _ := newImageTestApp(t, "red.png", 80, 40, color.RGBA{R: 255, A: 255})

	app.split = true
	app.splitRatio = defaultSplitRatio
	app.tabs2 = []string{"red.png"}
	app.active2 = 0
	app.focusGroup = 1
	app.refresh()

	view := app.View()
	if !view.Split {
		t.Fatal("view did not enter split mode")
	}

	if !view.Second.Image.On {
		t.Fatal("second pane image view is off")
	}

	if view.Second.Image.NaturalW != 80 || view.Second.Image.NaturalH != 40 {
		t.Fatalf("second pane natural size = %dx%d, want 80x40",
			view.Second.Image.NaturalW, view.Second.Image.NaturalH)
	}

	ctx := context.Background()
	if err := app.Page().Redraw(ctx); err != nil {
		t.Fatalf("redraw: %v", err)
	}

	if _, ok := boxByID(app.Page().Boxes(), "image-view-2"); !ok {
		t.Fatal("missing image-view-2 box")
	}

	canvas, ok := boxByID(app.Page().Boxes(), "image-canvas-2")
	if !ok {
		t.Fatal("missing image-canvas-2 box")
	}

	img := mustDecodePNG(t, app.Page().Probe().PNG())
	cx := int(canvas.X + canvas.W/2)
	cy := int(canvas.Y + canvas.H/2)
	r, g, b, _ := img.At(cx, cy).RGBA()

	if r>>8 < 200 || g>>8 > 60 || b>>8 > 60 {
		t.Fatalf("second canvas centre (%d,%d) = rgb(%d,%d,%d), want red", cx, cy, r>>8, g>>8, b>>8)
	}
}

// TestImageViewInvalidPNG checks a corrupt image leaves the image viewer off
// and reports the decoder error in the empty state.
func TestImageViewInvalidPNG(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Workspace\n")
	writeFile(t, dir, "broken.png", "not a png")

	app, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Cleanup(app.Close)

	app.openFile("broken.png")

	view := app.View()
	if view.Image.On {
		t.Fatal("invalid PNG turned the image view on")
	}

	if !view.Empty || view.EmptyKind != "error" {
		t.Fatalf("empty = %v kind = %q, want the error state", view.Empty, view.EmptyKind)
	}

	if strings.TrimSpace(view.EmptyBody) == "" {
		t.Fatal("invalid PNG has an empty error body")
	}
}

// TestImageViewBlocksEdit checks toggling edit mode cannot open an image as a
// text buffer.
func TestImageViewBlocksEdit(t *testing.T) {
	app, _ := newImageTestApp(t, "red.png", 80, 40, color.RGBA{R: 255, A: 255})

	app.toggleEdit()

	if app.editing {
		t.Fatal("image entered edit mode")
	}

	if app.View().Editing {
		t.Fatal("image view publishes edit mode")
	}
}

// TestImageViewTemplate checks the image template branch shows the canvas id
// and natural dimensions instead of a text line count.
func TestImageViewTemplate(t *testing.T) {
	app, _ := newImageTestApp(t, "red.png", 80, 40, color.RGBA{R: 255, A: 255})

	html := strings.Join(strings.Fields(renderAppHTML(t, app)), " ")

	if !strings.Contains(html, "image-canvas") {
		t.Fatal("rendered HTML has no image-canvas")
	}

	if !strings.Contains(html, "80 × 40") {
		t.Fatal("rendered HTML has no 80 × 40 dimensions")
	}

	if strings.Contains(html, "0 lines") {
		t.Fatal("rendered HTML shows 0 lines for the active image")
	}
}
