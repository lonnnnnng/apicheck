package presentation

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"strings"
	"testing"
)

func TestGenerateQRCodeDataURLReturnsSquarePNG(t *testing.T) {
	dataURL, err := GenerateQRCodeDataURL("https://example.com/v1,sk-test")
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(dataURL, prefix) {
		t.Fatalf("data URL prefix = %q", dataURL[:min(len(dataURL), len(prefix))])
	}
	imageData, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, prefix))
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(imageData))
	if err != nil {
		t.Fatal(err)
	}
	if config.Width <= 0 || config.Width != config.Height {
		t.Fatalf("QR dimensions = %dx%d, want non-zero square", config.Width, config.Height)
	}
}

func TestGenerateQRCodeDataURLRejectsEmptyContent(t *testing.T) {
	if _, err := GenerateQRCodeDataURL("  "); err == nil {
		t.Fatal("GenerateQRCodeDataURL() error = nil, want validation error")
	}
}
