package fs

import (
	"testing"
)

func newTestFsExclude(suffixes []string) *Fs {
	return &Fs{exclude: suffixSet(suffixes)}
}

func TestIsExcluded(t *testing.T) {
	f := newTestFsExclude([]string{".crdownload", ".part", "download"})
	tests := []struct {
		path string
		want bool
	}{
		{"/downloads/Unconfirmed 123.crdownload", true},
		{"/downloads/image.jpg.crdownload", true},
		{"/downloads/file.part", true},
		{"/downloads/file.download", true},
		{"/downloads/file.partial", false},
		{"/downloads/image.jpg", false},
		{"/downloads/file", false},
		{"/.crdownload", true},
	}
	for _, tt := range tests {
		if got := f.isExcluded(tt.path); got != tt.want {
			t.Errorf("isExcluded(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestIsExcludedEmpty(t *testing.T) {
	f := newTestFsExclude(nil)
	if f.isExcluded("/a/b.crdownload") {
		t.Fatalf("empty exclude should not match")
	}
}

func TestSuffixSetNormalization(t *testing.T) {
	m := suffixSet([]string{".PART", "TMP", ""})
	if !m[".part"] {
		t.Fatalf(".part should be lowercased")
	}
	if !m[".tmp"] {
		t.Fatalf(".tmp should get leading dot")
	}
	if len(m) != 2 {
		t.Fatalf("empty suffix should be skipped, got %v", m)
	}
}
