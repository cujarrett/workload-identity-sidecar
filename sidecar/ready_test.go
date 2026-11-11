package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReady(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "ready")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		paths []string
		want  int
	}{
		{"every file exists", []string{file}, 0},
		{"one file missing", []string{file, filepath.Join(dir, "missing")}, 1},
		{"a directory is not ready", []string{dir}, 1},
	}
	for _, c := range cases {
		if got := ready(c.paths); got != c.want {
			t.Errorf("%s: ready = %d, want %d", c.name, got, c.want)
		}
	}
}
