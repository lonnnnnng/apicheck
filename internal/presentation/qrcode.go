package presentation

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	qrcode "github.com/yeqown/go-qrcode/v2"
	"github.com/yeqown/go-qrcode/writer/standard"
)

type bufferWriteCloser struct {
	io.Writer
}

func (bufferWriteCloser) Close() error { return nil }

func GenerateQRCodeDataURL(content string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", errors.New("qr code content is required")
	}
	code, err := qrcode.NewWith(content, qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionMedium))
	if err != nil {
		return "", err
	}

	var image bytes.Buffer
	writer := standard.NewWithWriter(bufferWriteCloser{Writer: &image},
		standard.WithBuiltinImageEncoder(standard.PNG_FORMAT),
		standard.WithQRWidth(12),
		standard.WithBorderWidth(4),
	)
	if err := code.Save(writer); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(image.Bytes()), nil
}
