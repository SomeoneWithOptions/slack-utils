package users

import (
	"testing"
)

func TestIsResolvableUserID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{id: "U123", want: true},
		{id: "W123", want: true},
		{id: "B123", want: false},
		{id: "user@example.com", want: false},
		{id: "", want: false},
	}
	for _, tt := range tests {
		if got := IsResolvableUserID(tt.id); got != tt.want {
			t.Fatalf("IsResolvableUserID(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}
