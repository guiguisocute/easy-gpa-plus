package api

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestValidateCrestSVG(t *testing.T) {
	for _, svg := range []string{"", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 50"><path d="M0 0L100 50" fill="none" stroke="#123"/></svg>`, `<svg><defs><linearGradient id="g"><stop offset="0" stop-color="red"/></linearGradient></defs><rect width="10" height="10" fill="url(#g)"/></svg>`} {
		if err := validateCrestSVG(svg); err != nil {
			t.Fatalf("valid SVG: %v", err)
		}
	}
	for _, svg := range []string{`<svg onload="alert(1)"/>`, `<svg><script/></svg>`, `<svg><foreignObject/></svg>`, `<svg><image href="https://example.org/a.png"/></svg>`, `<!DOCTYPE svg><svg/>`, `<svg><path fill="url(https://example.org/a)"/></svg>`, `<svg><use href="#a"/></svg>`, `<svg/><svg/>`, `<svg>`, `<svg><path style="fill:red"/></svg>`, `<svg><animate/></svg>`} {
		if validateCrestSVG(svg) == nil {
			t.Fatalf("accepted unsafe SVG: %s", svg)
		}
	}
}

func TestCrestEmbeddedRaster(t *testing.T) {
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	data := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData.Bytes())
	for _, attribute := range []string{"href", "xlink:href"} {
		svg := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><image width="2" height="2" ` + attribute + `="` + data + `"/></svg>`
		if err := validateCrestSVG(svg); err != nil {
			t.Fatalf("embedded raster: %v", err)
		}
	}
	for _, data := range []string{
		"https://example.org/logo.png", "data:image/svg+xml;base64,PHN2Zy8+",
		"data:image/png;base64,broken", strings.Replace(data, "image/png", "image/jpeg", 1),
	} {
		if validateCrestSVG(`<svg><image href="`+data+`"/></svg>`) == nil {
			t.Fatal("unsafe or mislabeled raster accepted")
		}
	}
	pngData.Reset()
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 4097, 1))); err != nil {
		t.Fatal(err)
	}
	if validateCrestRaster("data:image/png;base64,"+base64.StdEncoding.EncodeToString(pngData.Bytes())) == nil {
		t.Fatal("oversized raster accepted")
	}
}
