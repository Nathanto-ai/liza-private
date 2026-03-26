package main

import "testing"

func TestInferSpecType(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"specs/vision.md", "vision"},
		{"specs/delivery.md", "delivery"},
		{"specs/VISION.md", "vision"},
		{"specs/my-vision-spec.md", "vision"},
		{"specs/feature.md", "delivery"},
		{"vision.md", "vision"},
		{"some/path/delivery-v2.md", "delivery"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := inferSpecType(tt.path)
			if got != tt.want {
				t.Errorf("inferSpecType(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
