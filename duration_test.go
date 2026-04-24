package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCustomDurationUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
		hasError bool
	}{
		{name: "seconds only", input: "30", expected: 30},
		{name: "minutes and seconds", input: "5:30", expected: 330},
		{name: "hours, minutes, and seconds", input: "1:30:45", expected: 5445},
		{name: "zero duration", input: "0", expected: 0},
		{name: "invalid format", input: "invalid", hasError: true},
		{name: "too many parts", input: "1:2:3:4", hasError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var duration CustomDuration
			jsonInput := fmt.Sprintf(`"%s"`, tt.input)
			err := json.Unmarshal([]byte(jsonInput), &duration)

			if tt.hasError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if int(duration) != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, int(duration))
			}
		})
	}
}

func BenchmarkCustomDurationUnmarshal(b *testing.B) {
	jsonInput := `"1:30:45"`

	for i := 0; i < b.N; i++ {
		var duration CustomDuration
		_ = json.Unmarshal([]byte(jsonInput), &duration)
	}
}
