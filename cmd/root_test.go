package cmd

import (
	"testing"
)

func TestPgParamValue(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		key     string
		val     string
		wantErr bool
	}{
		{"valid", "max_connections=200", "max_connections", "200", false},
		{"value with equals", "shared_buffers=256MB", "shared_buffers", "256MB", false},
		{"no equals", "max_connections", "", "", true},
		{"empty key", "=200", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := make(pgParamValue)
			err := p.Set(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if p[tt.key] != tt.val {
				t.Errorf("got %q=%q, want %q=%q", tt.key, p[tt.key], tt.key, tt.val)
			}
		})
	}
}

func TestPgParamValueMultiple(t *testing.T) {
	p := make(pgParamValue)
	if err := p.Set("max_connections=200"); err != nil {
		t.Fatal(err)
	}
	if err := p.Set("shared_buffers=256MB"); err != nil {
		t.Fatal(err)
	}
	if len(p) != 2 {
		t.Fatalf("expected 2 params, got %d", len(p))
	}
	if p["max_connections"] != "200" {
		t.Errorf("expected max_connections=200, got %s", p["max_connections"])
	}
	if p["shared_buffers"] != "256MB" {
		t.Errorf("expected shared_buffers=256MB, got %s", p["shared_buffers"])
	}
}

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
