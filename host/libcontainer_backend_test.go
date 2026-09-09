package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLayerMergedUsr(t *testing.T) {
	dir := t.TempDir()

	t.Run("merged-usr image", func(t *testing.T) {
		usrBin := filepath.Join(dir, "usr", "bin")
		if err := os.MkdirAll(usrBin, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("usr/bin", filepath.Join(dir, "bin")); err != nil {
			t.Fatal(err)
		}
		if !layerMergedUsr(dir) {
			t.Fatal("expected merged-usr detection to succeed for /bin -> usr/bin symlink")
		}
	})

	t.Run("legacy image with real bin dir", func(t *testing.T) {
		binDir := filepath.Join(dir, "legacy", "bin")
		if err := os.MkdirAll(binDir, 0755); err != nil {
			t.Fatal(err)
		}
		if layerMergedUsr(filepath.Join(dir, "legacy")) {
			t.Fatal("expected plain bin directory not to be treated as merged-usr")
		}
	})

	t.Run("bin symlink to something else", func(t *testing.T) {
		base := filepath.Join(dir, "other")
		if err := os.MkdirAll(base, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("sbin", filepath.Join(base, "bin")); err != nil {
			t.Fatal(err)
		}
		if layerMergedUsr(base) {
			t.Fatal("expected unknown symlink target not to be treated as merged-usr")
		}
	})

	t.Run("no bin at all", func(t *testing.T) {
		if layerMergedUsr(filepath.Join(dir, "empty")) {
			t.Fatal("expected missing bin not to be treated as merged-usr")
		}
	})
}
