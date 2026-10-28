package domain

import "testing"

func TestPNGDepthDetection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		content   []byte
		wantDepth byte
		wantValid bool
	}{
		{name: "8 bit", content: pngFixture(8), wantDepth: 8, wantValid: true},
		{name: "16 bit", content: pngFixture(16), wantDepth: 16, wantValid: true},
		{name: "invalid signature", content: []byte("not-a-png"), wantValid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			depth, valid := pngBitDepth(tt.content)
			if depth != tt.wantDepth || valid != tt.wantValid {
				t.Fatalf("pngBitDepth() = (%d, %v), want (%d, %v)", depth, valid, tt.wantDepth, tt.wantValid)
			}
		})
	}
}

func TestWebPDetection(t *testing.T) {
	t.Parallel()
	if !isWebP(append([]byte("RIFF1234WEBP"), []byte("VP8 ")...)) {
		t.Fatal("expected a RIFF WEBP payload to be accepted")
	}
	if isWebP([]byte("RIFF1234PNG ")) {
		t.Fatal("expected a non-WebP RIFF payload to be rejected")
	}
}

func pngFixture(bitDepth byte) []byte {
	content := make([]byte, 25)
	copy(content, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	copy(content[12:16], []byte("IHDR"))
	content[24] = bitDepth
	return content
}
