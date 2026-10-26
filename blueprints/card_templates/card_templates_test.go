package card_templates

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSaveUploadedImage_RejectsSVG(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("bg_image_file", "test.svg")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	_, _ = io.WriteString(part, "<svg><script>alert(1)</script></svg>")
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/card-templates/add", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	_, err = saveUploadedImage(req, "bg_image_file")
	if err == nil {
		t.Errorf("expected error rejecting .svg upload, but got nil")
	}
	if !strings.Contains(err.Error(), "SVG ممنوعة") && !strings.Contains(err.Error(), "غير مدعوم") {
		t.Errorf("expected SVG rejection error message, got: %v", err)
	}
}

func TestSaveUploadedImage_MIMEMismatch(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("bg_image_file", "fake.png")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	// Write plain text with PNG extension
	_, _ = io.WriteString(part, "This is plain text and not a valid PNG binary")
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/card-templates/add", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	_, err = saveUploadedImage(req, "bg_image_file")
	if err == nil {
		t.Errorf("expected error rejecting fake image with mismatching magic bytes, got nil")
	}
}

func TestSaveUploadedImage_ValidJPEG(t *testing.T) {
	defer os.RemoveAll("uploads")
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("bg_image_file", "valid.jpg")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	// Minimal valid JPEG header (SOI + APP0 marker)
	jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xFF, 0xD9}
	_, _ = part.Write(jpegHeader)
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/card-templates/add", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	path, err := saveUploadedImage(req, "bg_image_file")
	if err != nil {
		t.Fatalf("expected successful save of valid JPEG, got error: %v", err)
	}
	if !strings.HasPrefix(path, "/uploads/card_bg_") || !strings.HasSuffix(path, ".jpg") {
		t.Errorf("unexpected saved path format: %s", path)
	}
}
