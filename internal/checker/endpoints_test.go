package checker

import (
	"reflect"
	"testing"
)

func TestNormalizeRoots(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "host only", raw: "https://api.example.com", want: []string{"https://api.example.com", "https://api.example.com/v1"}},
		{name: "already v1", raw: "https://api.example.com/v1", want: []string{"https://api.example.com/v1"}},
		{name: "nested v1", raw: "https://api.example.com/proxy/v1", want: []string{"https://api.example.com/proxy/v1"}},
		{name: "chat endpoint", raw: "https://api.example.com/v1/chat/completions", want: []string{"https://api.example.com/v1"}},
		{name: "without scheme", raw: "api.example.com/v1/models", want: []string{"https://api.example.com/v1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeRoots(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("NormalizeRoots() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
