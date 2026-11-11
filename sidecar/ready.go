package main

import "os"

// ready is the startup probe. It exits 0 once every path is a regular file.
// It runs as a subcommand, so the probe needs no shell in the image.
func ready(paths []string) int {
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			return 1
		}
	}
	return 0
}
