package postgres

import (
	"strings"
	"testing"
)

func TestQuoteIdent(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"sourcedb", `"sourcedb"`},
		{"clone_123_ab", `"clone_123_ab"`},
		{`name"with"quotes`, `"name""with""quotes"`},
		{"", `""`},
	}

	for _, tt := range tests {
		got := quoteIdent(tt.input)
		if got != tt.expected {
			t.Errorf("quoteIdent(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestConnectionString(t *testing.T) {
	db := &DB{connString: "postgres://postgres:pass@localhost:5432/postgres?sslmode=disable"}
	if db.connString != "postgres://postgres:pass@localhost:5432/postgres?sslmode=disable" {
		t.Errorf("unexpected connString: %s", db.connString)
	}
}

func TestNewDB(t *testing.T) {
	connStr := "postgres://user:pass@host:5432/dbname?sslmode=disable"
	db := New(connStr)
	if db.connString != connStr {
		t.Errorf("expected connString %q, got %q", connStr, db.connString)
	}
	if db.pool != nil {
		t.Error("expected nil pool on new DB")
	}
}

func TestQuoteIdentPreventsSQLInjection(t *testing.T) {
	malicious := `db"; DROP TABLE users; --`
	quoted := quoteIdent(malicious)
	if !strings.HasPrefix(quoted, `"`) || !strings.HasSuffix(quoted, `"`) {
		t.Errorf("expected quoted identifier to be wrapped in double quotes: %s", quoted)
	}
	escaped := strings.ReplaceAll(malicious, `"`, `""`)
	expected := `"` + escaped + `"`
	if quoted != expected {
		t.Errorf("quoteIdent(%q) = %q, want %q", malicious, quoted, expected)
	}
}
