package mikrotik

import (
	"bytes"
	"testing"
)

func TestWordEncodingDecoding(t *testing.T) {
	testWords := []string{
		"",
		"test",
		"/login",
		"=name=admin",
		"=password=123456",
		"long_string_with_more_than_128_bytes_0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789",
	}

	for _, original := range testWords {
		var buf bytes.Buffer
		if err := writeWord(&buf, original); err != nil {
			t.Fatalf("writeWord failed for '%s': %v", original, err)
		}

		decoded, err := readWord(&buf)
		if err != nil {
			t.Fatalf("readWord failed for '%s': %v", original, err)
		}

		if decoded != original {
			t.Errorf("expected '%s', got '%s'", original, decoded)
		}
	}
}
