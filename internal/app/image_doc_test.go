package app

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImagePathDetection(t *testing.T) {
	t.Parallel()

	formats := map[string]string{
		"icon.png":         "PNG",
		"photos/IMAGE.PNG": "PNG",
		"portrait.jpg":     "JPEG",
		"portrait.JPEG":    "JPEG",
		"animation.gif":    "GIF",
		"vector/logo.svg":  "SVG",
		"vector/LOGO.SVG":  "SVG",
	}

	for path, want := range formats {
		if !isImagePath(path) {
			t.Errorf("%s not detected as an image", path)
		}

		got, ok := imageFormatOf(path)
		if !ok || got != want {
			t.Errorf("%s format = %q, %v; want %q, true", path, got, ok, want)
		}
	}

	for _, path := range []string{"photo.bmp", "photo.webp", "photo.ico", "notes.txt", "Makefile"} {
		if isImagePath(path) {
			t.Errorf("%s wrongly detected as an image", path)
		}

		if format, ok := imageFormatOf(path); ok || format != "" {
			t.Errorf("%s format = %q, %v; want empty, false", path, format, ok)
		}
	}
}

func TestImageSkipExtAndLanguage(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"photo.png", "photo.jpg", "photo.jpeg", "photo.gif", "photo.svg", "PHOTO.JPG"} {
		if skipExt(name) {
			t.Errorf("skipExt(%q) skipped a supported image", name)
		}

		if got := languageOf("assets/" + name); got != langImage {
			t.Errorf("languageOf(%q) = %s, want %s", name, got, langImage)
		}
	}

	for _, name := range []string{"photo.bmp", "photo.webp", "photo.ico", "archive.zip", "movie.mp4"} {
		if !skipExt(name) {
			t.Errorf("skipExt(%q) kept an unsupported binary", name)
		}
	}

	info := languageInfo(langImage)
	if info.Label != "Image" || info.Badge != "Img" {
		t.Fatalf("languageInfo(%s) = %+v, want Image/Img", langImage, info)
	}
}

func TestImageOpenPNG(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	raw := writeImagePNG(t, filepath.Join(dir, "pixel.png"), 7, 3)

	ws, err := loadDiskWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}

	if ws.ByPath["pixel.png"] == nil {
		t.Fatal("PNG was not scanned into the workspace")
	}

	doc := ws.openDoc("pixel.png")
	if doc.Err != "" {
		t.Fatalf("open PNG: %s", doc.Err)
	}

	if doc.Lang != langImage {
		t.Fatalf("lang = %s, want %s", doc.Lang, langImage)
	}

	if doc.Binary {
		t.Fatal("PNG marked binary")
	}

	if doc.Image == nil {
		t.Fatal("PNG has no image document")
	}

	if doc.Image.Width != 7 || doc.Image.Height != 3 {
		t.Fatalf("size = %dx%d, want 7x3", doc.Image.Width, doc.Image.Height)
	}

	if doc.Image.Format != "PNG" {
		t.Fatalf("format = %s, want PNG", doc.Image.Format)
	}

	if doc.Image.Src != imageSrcFor("pixel.png") {
		t.Fatalf("src = %q, want %q", doc.Image.Src, imageSrcFor("pixel.png"))
	}

	if !bytes.Equal(doc.Image.Bytes, raw) {
		t.Fatal("image bytes changed")
	}

	if len(doc.Lines) != 0 || len(doc.Tokens) != 0 {
		t.Fatalf("image kept %d lines and %d token rows", len(doc.Lines), len(doc.Tokens))
	}
}

func TestImageOpenSVGViewBox(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 25">` +
		`<rect width="40" height="25" fill="black"/></svg>`

	if err := os.WriteFile(filepath.Join(dir, "shape.svg"), []byte(svg), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := loadDiskWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}

	doc := ws.openDoc("shape.svg")
	if doc.Err != "" {
		t.Fatalf("open SVG: %s", doc.Err)
	}

	if doc.Lang != langImage || doc.Image == nil {
		t.Fatalf("SVG doc = %+v", doc)
	}

	if doc.Image.Width != 40 || doc.Image.Height != 25 {
		t.Fatalf("size = %dx%d, want 40x25", doc.Image.Width, doc.Image.Height)
	}

	if doc.Image.Format != "SVG" {
		t.Fatalf("format = %s, want SVG", doc.Image.Format)
	}
}

func TestImageSVGSize(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		svg  string
		want [2]int
	}{
		{
			name: "plain width and height",
			svg:  `<svg width="120" height="80"></svg>`,
			want: [2]int{120, 80},
		},
		{
			name: "units",
			svg:  `<svg width="2in" height="1in"></svg>`,
			want: [2]int{192, 96},
		},
		{
			name: "percentages fall back to viewBox",
			svg:  `<svg width="100%" height="100%" viewBox="0,0,20,10"></svg>`,
			want: [2]int{20, 10},
		},
		{
			name: "viewBox only",
			svg:  `<svg viewBox="0 0 64 48"></svg>`,
			want: [2]int{64, 48},
		},
		{
			name: "inline style",
			svg:  `<svg style="width:48px;height:32px" viewBox="0 0 64 48"></svg>`,
			want: [2]int{48, 32},
		},
		{
			name: "defaults",
			svg:  `<svg></svg>`,
			want: [2]int{svgDefaultWidth, svgDefaultHeight},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			width, height, err := svgSize([]byte(tc.svg))
			if err != nil {
				t.Fatalf("svgSize: %v", err)
			}

			if width != tc.want[0] || height != tc.want[1] {
				t.Fatalf("size = %dx%d, want %dx%d", width, height, tc.want[0], tc.want[1])
			}
		})
	}
}

func TestImageInvalid(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.png"), []byte("not a PNG"), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := loadDiskWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}

	doc := ws.openDoc("broken.png")
	if doc.Err == "" {
		t.Fatal("invalid PNG opened without an error")
	}

	if doc.Lang != langImage {
		t.Fatalf("lang = %s, want %s", doc.Lang, langImage)
	}

	if doc.Image != nil {
		t.Fatal("invalid PNG kept an image document")
	}
}

func TestImagePixelLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svg := `<svg width="5000" height="5000"></svg>`

	if err := os.WriteFile(filepath.Join(dir, "huge.svg"), []byte(svg), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, err := loadDiskWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}

	doc := ws.openDoc("huge.svg")
	if doc.Err == "" || !strings.Contains(doc.Err, "megapixel") {
		t.Fatalf("pixel-limit error = %q", doc.Err)
	}
}

func TestImageTooLarge(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "huge.png")

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := file.Truncate(maxImageBytes + 1); err != nil {
		t.Fatal(err)
	}

	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	ws, err := loadDiskWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}

	doc := ws.openDoc("huge.png")
	if doc.Err == "" || !strings.Contains(doc.Err, "larger than") {
		t.Fatalf("size-limit error = %q", doc.Err)
	}
}

func TestImageSrcFor(t *testing.T) {
	t.Parallel()

	first := imageSrcFor("assets/logo.png")
	if first != imageSrcFor("assets/logo.png") {
		t.Fatal("image source is not stable")
	}

	if !strings.HasPrefix(first, "owncode-image-") {
		t.Fatalf("source = %q, want owncode-image- prefix", first)
	}

	if strings.Contains(first, ":") {
		t.Fatalf("source %q contains a colon", first)
	}

	if first == imageSrcFor("assets/other.png") {
		t.Fatal("different paths share an image source")
	}
}

func TestImageFitSize(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                       string
		natW, natH, availW, availH int
		wantW, wantH               int
	}{
		{name: "zero natural", natW: 0, natH: 10, availW: 100, availH: 100, wantW: 0, wantH: 0},
		{name: "zero available", natW: 10, natH: 10, availW: 0, availH: 100, wantW: 0, wantH: 0},
		{name: "fits without upscaling", natW: 10, natH: 5, availW: 100, availH: 100, wantW: 10, wantH: 5},
		{name: "width limited", natW: 1000, natH: 500, availW: 200, availH: 200, wantW: 200, wantH: 100},
		{name: "height limited", natW: 500, natH: 1000, availW: 200, availH: 200, wantW: 100, wantH: 200},
		{name: "floors the shorter side", natW: 3, natH: 2, availW: 2, availH: 2, wantW: 2, wantH: 1},
		{name: "clamps to one pixel", natW: 1, natH: 1000, availW: 1000, availH: 500, wantW: 1, wantH: 500},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			width, height := fitImageSize(tc.natW, tc.natH, tc.availW, tc.availH)
			if width != tc.wantW || height != tc.wantH {
				t.Fatalf("fitImageSize(%d,%d,%d,%d) = %d,%d; want %d,%d",
					tc.natW, tc.natH, tc.availW, tc.availH,
					width, height, tc.wantW, tc.wantH)
			}
		})
	}
}

// writeImagePNG encodes a real PNG so openDoc exercises the registered
// standard-library decoder rather than a stub header.
func writeImagePNG(t *testing.T, path string, width, height int) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}
