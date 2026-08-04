package fs

import "testing"

func TestResolveRoot(t *testing.T) {
	c := newCaseMap()
	if got := c.Resolve(""); got != "" {
		t.Fatalf("Resolve(\"\") = %q, want \"\"", got)
	}
}

func TestResolveBasic(t *testing.T) {
	c := newCaseMap()
	c.Update("", []string{"DirA", "file.txt"})
	c.Update("DirA", []string{"SubDir", "inner.txt"})
	tests := []struct {
		in   string
		want string
	}{
		{"dira", "DirA"},
		{"DIRA", "DirA"},
		{"FILE.TXT", "file.txt"},
		{"dirA/subdir", "DirA/SubDir"},
		{"dira/SUBdir/inner.txt", "DirA/SubDir/inner.txt"},
		{"unknown", "unknown"},
		{"dira/missing.txt", "DirA/missing.txt"},
	}
	for _, tt := range tests {
		if got := c.Resolve(tt.in); got != tt.want {
			t.Errorf("Resolve(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveNestedCase(t *testing.T) {
	c := newCaseMap()
	c.Update("", []string{"A"})
	c.Update("A", []string{"B"})
	c.Update("A/B", []string{"File.TXT"})
	if got := c.Resolve("a/b/file.txt"); got != "A/B/File.TXT" {
		t.Fatalf("nested resolve = %q, want %q", got, "A/B/File.TXT")
	}
}

func TestLookup(t *testing.T) {
	c := newCaseMap()
	c.Update("dir", []string{"X", "Yy"})
	if actual, ok := c.Lookup("dir", "yy"); !ok || actual != "Yy" {
		t.Fatalf("Lookup(yy) = %q,%v", actual, ok)
	}
	if _, ok := c.Lookup("dir", "zz"); ok {
		t.Fatalf("Lookup(zz) should miss")
	}
	if _, ok := c.Lookup("other", "x"); ok {
		t.Fatalf("Lookup in unknown dir should miss")
	}
}
