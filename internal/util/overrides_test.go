package util

import (
	"testing"
)

func TestApplyOverrides(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		overrides  []Override
		wantResult string
	}{
		{
			name:  "Simple replacement",
			input: "hello world",
			overrides: []Override{
				{Pattern: "hello", Replacement: "hi"},
			},
			wantResult: "hi world",
		},
		{
			name:  "Multiple rules (first match wins)",
			input: "hello world",
			overrides: []Override{
				{Pattern: "hello", Replacement: "hi"},
				{Pattern: "world", Replacement: "earth"},
			},
			wantResult: "hi world",
		},
		{
			name:  "Named capture group replacement",
			input: "看看我的cnid是什么",
			overrides: []Override{
				{Pattern: `^看看我的(?P<server>cn|jp|tw|en|kr)id是什么$`, Replacement: `${server}id`},
			},
			wantResult: "cnid",
		},
		{
			name:  "No match",
			input: "something else",
			overrides: []Override{
				{Pattern: "hello", Replacement: "hi"},
			},
			wantResult: "something else",
		},
		{
			name:  "Invalid regex (should be ignored)",
			input: "test",
			overrides: []Override{
				{Pattern: "[", Replacement: "hi"},
			},
			wantResult: "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ApplyOverrides(tt.input, tt.overrides); got != tt.wantResult {
				t.Errorf("ApplyOverrides() = %v, want %v", got, tt.wantResult)
			}
		})
	}
}
