package cmd

import (
	"testing"
)

func TestStripDebug(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected []string
	}{
		{"no flags", []string{"--pg-port", "5432"}, []string{"--pg-port", "5432"}},
		{"debug only", []string{"--debug"}, []string{}},
		{"debug at start", []string{"--debug", "--pg-port", "5432"}, []string{"--pg-port", "5432"}},
		{"debug at end", []string{"--pg-port", "5432", "--debug"}, []string{"--pg-port", "5432"}},
		{"debug in middle", []string{"--pg-port", "--debug", "--name", "foo"}, []string{"--pg-port", "--name", "foo"}},
		{"multiple debug", []string{"--debug", "--debug", "--pg-port", "5432"}, []string{"--pg-port", "5432"}},
		{"empty args", []string{}, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripDebug(tt.args)
			if len(got) != len(tt.expected) {
				t.Errorf("stripDebug(%v) = %v, want %v", tt.args, got, tt.expected)
				return
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("stripDebug(%v) = %v, want %v", tt.args, got, tt.expected)
					return
				}
			}
		})
	}
}

func TestSetupLogger(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantDebug bool
	}{
		{"no debug flag", []string{"--pg-port", "5432"}, false},
		{"has debug flag", []string{"--debug", "--pg-port", "5432"}, true},
		{"only debug flag", []string{"--debug"}, true},
		{"empty args", []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := setupLogger(tt.args)
			if got != tt.wantDebug {
				t.Errorf("setupLogger(%v) = %v, want %v", tt.args, got, tt.wantDebug)
			}
		})
	}
}
