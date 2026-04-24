package main

import (
	"strings"
	"testing"
)

func TestSanitizeFileName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "empty string", input: "", expected: ""},
		{name: "simple title", input: "My Video", expected: "My Video"},
		{name: "title with unsafe characters", input: "My/Video:Test|File", expected: "My-Video-Test-File"},
		{name: "title with removed characters", input: "What? Why* How", expected: "What Why How"},
		{name: "title with control characters", input: "Hello\x00World\x1F", expected: "HelloWorld"},
		{name: "title with leading/trailing dots and spaces", input: "  ..My Video.. ", expected: "My Video"},
		{name: "title with multiple spaces", input: "My   Video   Title", expected: "My Video Title"},
		{name: "title that sanitizes to empty", input: "???***", expected: ""},
		{
			name:     "long ASCII title truncated at word boundary",
			input:    "This is a very long video title that needs to be truncated because it exceeds the maximum allowed character limit for filenames",
			expected: "This is a very long video title that needs to be truncated because it exceeds the maximum allowed",
		},
		{
			name:     "long multibyte title truncates by characters not bytes",
			input:    strings.Repeat("日本語", 40),
			expected: strings.Repeat("日本語", 33) + "日",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeFileName(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeFileName(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}
