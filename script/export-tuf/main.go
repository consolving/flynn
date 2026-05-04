// export-tuf is a standalone tool for populating a TUF repository with Flynn
// artifacts, without requiring a running Flynn cluster.
//
// It reads pre-built binaries, constructs squashfs layers, builds ImageManifests
// and Artifacts, generates images.json and bootstrap-manifest.json, and stages
// everything as signed TUF targets.
//
// Usage:
//
//	export-tuf --tuf-dir=/path/to/flynn-tuf-repo \
//	           --build-dir=/path/to/build \
//	           --source-dir=/path/to/flynn \
//	           --version=v20250412.0 \
//	           --layer-cache=/path/to/layer-cache
package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	ct "github.com/flynn/flynn/controller/types"
	tuf "github.com/flynn/go-tuf"
	tufdata "github.com/flynn/go-tuf/data"
	"github.com/flynn/go-tuf/util"
)

// imageSpec defines a Flynn component image: what base layers it inherits,
// what binaries and files it contains, and its entrypoint.
type imageSpec struct {
	Name          string
	Base          string            // base image name for layer inheritance
	Binaries      map[string]string // source binary name -> dest path in squashfs
	ExtraFiles    map[string]string // source file (relative to source-dir) -> dest path
	ExtraDirs     map[string]string // source dir (relative to source-dir) -> dest path
	Entrypoint    *ct.ImageEntrypoint
	PackageScript string // path relative to source-dir for package install script (run in chroot on base layer)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		tufDir      string
		buildDir    string
		sourceDir   string
		version     string
		layerCache  string
		tufRepo     string
		skipBase    bool
		pkgLayerDir string
	)

	// Parse flags manually (avoid adding dependencies)
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "--tuf-dir=") {
			tufDir = strings.TrimPrefix(arg, "--tuf-dir=")
		} else if strings.HasPrefix(arg, "--build-dir=") {
			buildDir = strings.TrimPrefix(arg, "--build-dir=")
		} else if strings.HasPrefix(arg, "--source-dir=") {
			sourceDir = strings.TrimPrefix(arg, "--source-dir=")
		} else if strings.HasPrefix(arg, "--version=") {
			version = strings.TrimPrefix(arg, "--version=")
		} else if strings.HasPrefix(arg, "--layer-cache=") {
			layerCache = strings.TrimPrefix(arg, "--layer-cache=")
		} else if strings.HasPrefix(arg, "--tuf-repo=") {
			tufRepo = strings.TrimPrefix(arg, "--tuf-repo=")
		} else if arg == "--skip-base-layers" {
			skipBase = true
		} else if strings.HasPrefix(arg, "--package-layer-dir=") {
			pkgLayerDir = strings.TrimPrefix(arg, "--package-layer-dir=")
		} else if arg == "--help" || arg == "-h" {
			printUsage()
			return nil
		}
	}

	if tufDir == "" || buildDir == "" || sourceDir == "" || version == "" || layerCache == "" {
		printUsage()
		return fmt.Errorf("missing required flags")
	}
	if tufRepo == "" {
		tufRepo = "https://consolving.github.io/flynn-tuf-repo/repository"
	}

	binDir := filepath.Join(buildDir, "bin")

	// Verify directories exist
	for _, dir := range []string{tufDir, binDir, sourceDir, layerCache} {
		if _, err := os.Stat(dir); err != nil {
			return fmt.Errorf("directory does not exist: %s", dir)
		}
	}

	e := &exporter{
		tufDir:         tufDir,
		buildDir:       buildDir,
		binDir:         binDir,
		sourceDir:      sourceDir,
		version:        version,
		layerCache:     layerCache,
		tufRepoURL:     tufRepo,
		layerURLTpl:    fmt.Sprintf("https://github.com/consolving/flynn-tuf-repo/releases/download/%s/{id}.squashfs", version),
		skipBaseLayers: skipBase,
		pkgLayerDir:    pkgLayerDir,
		baseLayers:     make(map[string][]*ct.ImageLayer),
		packageLayers:  make(map[string]*ct.ImageLayer),
		artifacts:      make(map[string]*ct.Artifact),
	}

	return e.Run()
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: export-tuf [options]

Options:
  --tuf-dir=DIR       Path to TUF repository (with keys/ and repository/)
  --build-dir=DIR     Path to build output directory (with bin/)
  --source-dir=DIR    Path to Flynn source directory
  --version=VERSION   Version string (e.g., v20250412.0)
  --layer-cache=DIR   Path to layer cache directory
  --tuf-repo=URL      TUF repository URL [default: https://consolving.github.io/flynn-tuf-repo/repository]
  --skip-base-layers  Use cached base layers instead of building them
  --package-layer-dir=DIR  Directory with pre-built {name}-packages.squashfs files
`)
}

type exporter struct {
	tufDir         string
	buildDir       string
	binDir         string
	sourceDir      string
	version        string
	layerCache     string
	tufRepoURL     string
	layerURLTpl    string
	skipBaseLayers bool
	pkgLayerDir    string

	baseLayers    map[string][]*ct.ImageLayer // base image name -> accumulated layers
	packageLayers map[string]*ct.ImageLayer   // package script path -> cached layer
	artifacts     map[string]*ct.Artifact     // image name -> artifact
}

func (e *exporter) Run() error {
	fmt.Printf("=== Flynn TUF Export ===\n")
	fmt.Printf("Version:     %s\n", e.version)
	fmt.Printf("TUF dir:     %s\n", e.tufDir)
	fmt.Printf("Build dir:   %s\n", e.buildDir)
	fmt.Printf("Source dir:  %s\n", e.sourceDir)
	fmt.Printf("Layer cache: %s\n", e.layerCache)
	fmt.Printf("TUF repo:    %s\n", e.tufRepoURL)
	fmt.Printf("\n")

	// Step 1: Build base OS squashfs layers
	fmt.Printf("--- Step 1: Building base OS layers ---\n")
	if err := e.buildBaseLayers(); err != nil {
		return fmt.Errorf("building base layers: %s", err)
	}

	// Step 2: Build component squashfs layers and construct artifacts
	fmt.Printf("\n--- Step 2: Building component images ---\n")
	if err := e.buildComponentImages(); err != nil {
		return fmt.Errorf("building component images: %s", err)
	}

	// Step 3: Generate images.json and bootstrap-manifest.json
	fmt.Printf("\n--- Step 3: Generating manifests ---\n")
	if err := e.generateManifests(); err != nil {
		return fmt.Errorf("generating manifests: %s", err)
	}

	// Step 4: Stage TUF targets and sign
	fmt.Printf("\n--- Step 4: Staging TUF targets ---\n")
	if err := e.stageTUFTargets(); err != nil {
		return fmt.Errorf("staging TUF targets: %s", err)
	}

	fmt.Printf("\n=== Export complete! ===\n")
	return nil
}

// ----- Step 1: Build base OS layers -----

func (e *exporter) buildBaseLayers() error {
	// Build the base OS layers in dependency order.
	bases := []struct {
		name   string
		script string
	}{
		{"busybox", "builder/img/busybox.sh"},
		{"ubuntu-noble", "builder/img/ubuntu-noble.sh"},
	}

	for _, base := range bases {
		if e.skipBaseLayers {
			// Load from cache instead of building
			fmt.Printf("  Loading base layer from cache: %s\n", base.name)
			layer, err := e.loadBaseLayerFromCache(base.name)
			if err != nil {
				return fmt.Errorf("loading cached %s: %s (try without --skip-base-layers)", base.name, err)
			}
			e.baseLayers[base.name] = []*ct.ImageLayer{layer}
			fmt.Printf("  -> %s: id=%s size=%d\n", base.name, layer.ID, layer.Length)
		} else {
			fmt.Printf("  Building base layer: %s\n", base.name)
			layer, err := e.buildBaseLayer(base.name, base.script)
			if err != nil {
				return fmt.Errorf("building %s: %s", base.name, err)
			}
			e.baseLayers[base.name] = []*ct.ImageLayer{layer}
			fmt.Printf("  -> %s: id=%s size=%d\n", base.name, layer.ID, layer.Length)
		}
	}

	return nil
}

// loadBaseLayerFromCache finds the cached layer for the given base name.
// It uses the known base layer hashes from the layer cache's JSON metadata.
func (e *exporter) loadBaseLayerFromCache(name string) (*ct.ImageLayer, error) {
	// Scan the cache for layers and check their JSON metadata to identify base layers.
	// Base layers built by buildBaseLayer are large squashfs files.
	// For busybox: ~2.5 MB with /bin/busybox, /etc/passwd
	// For ubuntu-noble: ~158 MB with full Ubuntu rootfs

	entries, err := os.ReadDir(e.layerCache)
	if err != nil {
		return nil, err
	}

	var bestID string
	var bestSize int64
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".squashfs") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".squashfs")
		size := info.Size()

		if name == "ubuntu-noble" {
			// Ubuntu Noble base is ~150-170 MB, has /etc/cloud/ directory
			if size > 100*1024*1024 {
				sqfsPath := filepath.Join(e.layerCache, entry.Name())
				if isUbuntuNobleBase(sqfsPath) && size > bestSize {
					bestSize = size
					bestID = id
				}
			}
		} else if name == "busybox" {
			// Busybox base is ~2-3 MB (not 4KB which would be slugrunner)
			if size > 1*1024*1024 && size < 10*1024*1024 {
				// Verify it's actually busybox by checking it has /bin/busybox
				sqfsPath := filepath.Join(e.layerCache, entry.Name())
				if isBusyboxLayer(sqfsPath) {
					if bestSize == 0 || size > bestSize {
						bestSize = size
						bestID = id
					}
				}
			}
		}
	}

	if bestID == "" {
		return nil, fmt.Errorf("no cached layer found for %s", name)
	}

	// Read the cached layer and verify hash
	squashfsPath := filepath.Join(e.layerCache, bestID+".squashfs")
	data, err := os.ReadFile(squashfsPath)
	if err != nil {
		return nil, err
	}

	digest := sha512.Sum512_256(data)
	computedID := hex.EncodeToString(digest[:])
	if computedID != bestID {
		return nil, fmt.Errorf("cache integrity error: expected %s got %s", bestID, computedID)
	}

	return &ct.ImageLayer{
		ID:     bestID,
		Type:   ct.ImageLayerTypeSquashfs,
		Length: int64(len(data)),
		Hashes: map[string]string{
			"sha512_256": bestID,
		},
	}, nil
}

// isBusyboxLayer checks if a squashfs file contains /bin/busybox
func isBusyboxLayer(sqfsPath string) bool {
	// Use unsquashfs to check for busybox binary
	cmd := exec.Command("unsquashfs", "-l", sqfsPath)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "/bin/busybox")
}

// isUbuntuNobleBase checks if a squashfs is an Ubuntu Noble cloud-image base layer
// (as opposed to a package-install diff layer that might also be large)
func isUbuntuNobleBase(sqfsPath string) bool {
	cmd := exec.Command("unsquashfs", "-l", sqfsPath)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	// Cloud images have /etc/cloud/ directory; package diff layers don't
	return strings.Contains(string(out), "/etc/cloud/")
}

func (e *exporter) buildBaseLayer(name, scriptPath string) (*ct.ImageLayer, error) {
	scriptAbs := filepath.Join(e.sourceDir, scriptPath)

	// Create a temporary output directory for the squashfs
	outDir, err := os.MkdirTemp("", "flynn-base-"+name)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(outDir)

	outFile := filepath.Join(outDir, "layer.squashfs")

	// The base image scripts expect to write to /mnt/out/layer.squashfs
	// and be run from the flynn source root. We'll create a wrapper that
	// sets up the environment.
	wrapper := fmt.Sprintf(`#!/bin/bash
set -e
mkdir -p /mnt/out
rm -f /mnt/out/layer.squashfs
cd %q
bash %q
cp /mnt/out/layer.squashfs %q
`, e.sourceDir, scriptAbs, outFile)

	cmd := exec.Command("bash", "-c", wrapper)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = e.sourceDir
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("running %s: %s", scriptPath, err)
	}

	return e.importSquashfs(outFile)
}

// importSquashfs reads a squashfs file, computes its hash, copies it to the
// layer cache, and returns an ImageLayer.
func (e *exporter) importSquashfs(squashfsPath string) (*ct.ImageLayer, error) {
	data, err := os.ReadFile(squashfsPath)
	if err != nil {
		return nil, err
	}

	digest := sha512.Sum512_256(data)
	id := hex.EncodeToString(digest[:])

	// Copy to layer cache
	cachePath := filepath.Join(e.layerCache, id+".squashfs")
	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		return nil, err
	}

	layer := &ct.ImageLayer{
		ID:     id,
		Type:   ct.ImageLayerTypeSquashfs,
		Length: int64(len(data)),
		Hashes: map[string]string{
			"sha512_256": id,
		},
	}

	// Write layer config JSON
	configPath := filepath.Join(e.layerCache, id+".json")
	configData, err := json.Marshal(layer)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(configPath, configData, 0644); err != nil {
		return nil, err
	}

	return layer, nil
}

// ----- Step 2: Build component images -----

func (e *exporter) buildComponentImages() error {
	specs := e.imageSpecs()

	for _, spec := range specs {
		fmt.Printf("  Building image: %s\n", spec.Name)
		if err := e.buildComponentImage(spec); err != nil {
			return fmt.Errorf("building %s: %s", spec.Name, err)
		}
	}

	return nil
}

func (e *exporter) buildComponentImage(spec imageSpec) error {
	// Create a temporary directory with the component's file layout
	tmpDir, err := os.MkdirTemp("", "flynn-img-"+spec.Name)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// For ubuntu-noble based images, /bin is a symlink to usr/bin.
	// Component layers must place files in /usr/bin/ to avoid creating
	// a real /bin/ directory that shadows the base layer's symlink in overlayfs.
	remapBin := spec.Base == "ubuntu-noble"

	// Copy binaries
	for srcName, destPath := range spec.Binaries {
		srcPath := filepath.Join(e.binDir, srcName)
		if remapBin && strings.HasPrefix(destPath, "/bin/") {
			destPath = "/usr" + destPath
		}
		dst := filepath.Join(tmpDir, destPath)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		if err := copyFile(srcPath, dst, 0755); err != nil {
			return fmt.Errorf("copying binary %s: %s", srcName, err)
		}
	}

	// Copy extra files
	for srcRel, destPath := range spec.ExtraFiles {
		srcPath := filepath.Join(e.sourceDir, srcRel)
		if remapBin && strings.HasPrefix(destPath, "/bin/") {
			destPath = "/usr" + destPath
		}
		dst := filepath.Join(tmpDir, destPath)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		if err := copyFile(srcPath, dst, 0755); err != nil {
			return fmt.Errorf("copying extra file %s: %s", srcRel, err)
		}
	}

	// Copy extra directories
	for srcRel, destPath := range spec.ExtraDirs {
		srcPath := filepath.Join(e.sourceDir, srcRel)
		dstBase := filepath.Join(tmpDir, destPath)
		err := filepath.Walk(srcPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(srcPath, path)
			dst := filepath.Join(dstBase, rel)
			if info.IsDir() {
				return os.MkdirAll(dst, 0755)
			}
			// Skip macOS resource fork files
			if strings.HasPrefix(filepath.Base(path), "._") {
				return nil
			}
			return copyFile(path, dst, 0644)
		})
		if err != nil {
			return fmt.Errorf("copying extra dir %s: %s", srcRel, err)
		}
	}

	// Create squashfs from the directory
	squashfsPath := filepath.Join(tmpDir, "layer.squashfs")
	cmd := exec.Command("mksquashfs", tmpDir, squashfsPath, "-noappend",
		"-e", "layer.squashfs") // exclude the output file itself
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mksquashfs: %s", err)
	}

	// Import the squashfs
	componentLayer, err := e.importSquashfs(squashfsPath)
	if err != nil {
		return err
	}

	// Build the ImageManifest with base layers + optional package layer + component layer
	var allLayers []*ct.ImageLayer
	if baseLayers, ok := e.baseLayers[spec.Base]; ok {
		allLayers = append(allLayers, baseLayers...)
	}
	if spec.PackageScript != "" {
		pkgLayer, err := e.buildPackageLayer(spec)
		if err != nil {
			return fmt.Errorf("building package layer for %s: %s", spec.Name, err)
		}
		allLayers = append(allLayers, pkgLayer)
	}
	allLayers = append(allLayers, componentLayer)

	manifest := ct.ImageManifest{
		Type: ct.ImageManifestTypeV1,
		Rootfs: []*ct.ImageRootfs{{
			Platform: ct.DefaultImagePlatform,
			Layers:   allLayers,
		}},
	}
	if spec.Entrypoint != nil {
		manifest.Entrypoints = map[string]*ct.ImageEntrypoint{
			"_default": spec.Entrypoint,
		}
	}

	imageURL := fmt.Sprintf("%s?name=%s&target=/images/%s.json", e.tufRepoURL, spec.Name, manifest.ID())
	artifact := &ct.Artifact{
		Type:             ct.ArtifactTypeFlynn,
		URI:              imageURL,
		RawManifest:      manifest.RawManifest(),
		Hashes:           manifest.Hashes(),
		Size:             int64(len(manifest.RawManifest())),
		LayerURLTemplate: e.layerURLTpl,
		Meta: map[string]string{
			"manifest.id":        manifest.ID(),
			"flynn.component":    spec.Name,
			"flynn.system-image": "true",
		},
	}

	e.artifacts[spec.Name] = artifact
	fmt.Printf("  -> %s: manifest=%s layers=%d\n", spec.Name, manifest.ID()[:16], len(allLayers))

	return nil
}

// buildPackageLayer runs a package installation script in a chroot on the base layer,
// producing a squashfs layer containing only the changes (installed packages, users, etc.).
// Results are cached by package script path so multiple images sharing the same script
// only build the layer once.
func (e *exporter) buildPackageLayer(spec imageSpec) (*ct.ImageLayer, error) {
	// Check cache
	if layer, ok := e.packageLayers[spec.PackageScript]; ok {
		fmt.Printf("    Using cached package layer for %s\n", spec.PackageScript)
		return layer, nil
	}

	fmt.Printf("    Building package layer: %s\n", spec.PackageScript)

	// Check for pre-built package layer in pkgLayerDir
	if e.pkgLayerDir != "" {
		prebuiltPath := filepath.Join(e.pkgLayerDir, spec.Name+"-packages.squashfs")
		if _, err := os.Stat(prebuiltPath); err == nil {
			fmt.Printf("    Using pre-built package layer: %s\n", prebuiltPath)
			layer, err := e.importSquashfs(prebuiltPath)
			if err != nil {
				return nil, fmt.Errorf("importing pre-built package layer: %s", err)
			}
			e.packageLayers[spec.PackageScript] = layer
			fmt.Printf("    -> package layer: id=%s size=%d\n", layer.ID[:16], layer.Length)
			return layer, nil
		}
	}

	// Find the base layer squashfs file
	baseLayers := e.baseLayers[spec.Base]
	if len(baseLayers) == 0 {
		return nil, fmt.Errorf("no base layers for %s", spec.Base)
	}
	baseLayerID := baseLayers[0].ID
	baseSquashfs := filepath.Join(e.layerCache, baseLayerID+".squashfs")

	// Create temp dirs for overlay mount
	workDir, err := os.MkdirTemp("", "flynn-pkg-"+spec.Name)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir)

	lowerDir := filepath.Join(workDir, "lower")
	upperDir := filepath.Join(workDir, "upper")
	mergedDir := filepath.Join(workDir, "merged")
	overlayWork := filepath.Join(workDir, "work")
	for _, d := range []string{lowerDir, upperDir, mergedDir, overlayWork} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return nil, err
		}
	}

	// Mount base squashfs
	cmd := exec.Command("mount", "-t", "squashfs", "-o", "loop,ro", baseSquashfs, lowerDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("mount base squashfs: %s", err)
	}
	defer exec.Command("umount", "-l", lowerDir).Run()

	// Mount overlayfs
	overlayOpts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerDir, upperDir, overlayWork)
	cmd = exec.Command("mount", "-t", "overlay", "overlay", "-o", overlayOpts, mergedDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("mount overlay: %s", err)
	}
	defer exec.Command("umount", "-l", mergedDir).Run()

	// Bind-mount essential filesystems for chroot
	for _, m := range []struct{ src, dst string }{
		{"/proc", filepath.Join(mergedDir, "proc")},
		{"/sys", filepath.Join(mergedDir, "sys")},
		{"/dev", filepath.Join(mergedDir, "dev")},
	} {
		os.MkdirAll(m.dst, 0755)
		cmd = exec.Command("mount", "--bind", m.src, m.dst)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("bind mount %s: %s", m.src, err)
		}
		defer exec.Command("umount", "-l", m.dst).Run()
	}

	// Copy resolv.conf for network access during package install
	resolvSrc := "/etc/resolv.conf"
	resolvDst := filepath.Join(mergedDir, "etc/resolv.conf")
	copyFile(resolvSrc, resolvDst, 0644)

	// Copy the package script into the chroot
	scriptSrc := filepath.Join(e.sourceDir, spec.PackageScript)
	scriptDst := filepath.Join(mergedDir, "tmp/packages.sh")
	os.MkdirAll(filepath.Dir(scriptDst), 0755)
	if err := copyFile(scriptSrc, scriptDst, 0755); err != nil {
		return nil, fmt.Errorf("copying package script: %s", err)
	}

	// Run the package script in chroot
	cmd = exec.Command("chroot", mergedDir, "/bin/bash", "/tmp/packages.sh")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "DEBIAN_FRONTEND=noninteractive"}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("running package script: %s", err)
	}

	// Clean up: remove the script, apt lists, and tmp files from upper
	os.Remove(filepath.Join(upperDir, "tmp/packages.sh"))
	os.RemoveAll(filepath.Join(upperDir, "var/lib/apt/lists"))
	os.RemoveAll(filepath.Join(upperDir, "var/cache/apt"))
	os.RemoveAll(filepath.Join(upperDir, "tmp"))

	// Remove overlayfs whiteout/opaque markers for directories we don't want to shadow
	// The upper dir now contains all the changes from package installation
	// We need to clean overlayfs-specific xattrs before creating squashfs
	// Actually, we want the raw upper dir content — overlayfs whiteouts are
	// char devices (0,0) that mksquashfs will include, but they won't work
	// as intended outside overlayfs. For package installs, we shouldn't have
	// deletions, so whiteouts should be minimal. Let's just remove them.
	exec.Command("find", upperDir, "-type", "c", "-delete").Run()

	// Create squashfs from the upper directory (the diff)
	squashfsPath := filepath.Join(workDir, "package-layer.squashfs")
	cmd = exec.Command("mksquashfs", upperDir, squashfsPath, "-noappend")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("mksquashfs package layer: %s", err)
	}

	// Import the squashfs layer
	layer, err := e.importSquashfs(squashfsPath)
	if err != nil {
		return nil, err
	}

	// Cache it
	e.packageLayers[spec.PackageScript] = layer
	fmt.Printf("    -> package layer: id=%s size=%d\n", layer.ID[:16], layer.Length)

	return layer, nil
}

// imageSpecs returns the specifications for all component images.
func (e *exporter) imageSpecs() []imageSpec {
	return []imageSpec{
		// --- busybox-based images ---
		{
			Name: "discoverd",
			Base: "busybox",
			Binaries: map[string]string{
				"discoverd": "/bin/discoverd",
			},
			ExtraFiles: map[string]string{
				"discoverd/start.sh": "/bin/start-discoverd",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/start-discoverd"},
			},
		},
		{
			Name: "flannel",
			Base: "busybox",
			Binaries: map[string]string{
				"flanneld":        "/bin/flanneld",
				"flannel-wrapper": "/bin/flannel-wrapper",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/flannel-wrapper"},
			},
		},
		{
			Name: "controller",
			Base: "busybox",
			Binaries: map[string]string{
				"flynn-controller": "/bin/flynn-controller",
				"flynn-scheduler":  "/bin/flynn-scheduler",
				"flynn-worker":     "/bin/flynn-worker",
			},
			ExtraFiles: map[string]string{
				"controller/start.sh":        "/bin/start-flynn-controller",
				"util/ca-certs/ca-certs.pem": "/etc/ssl/certs/ca-certs.pem",
				"schema/common.json":         "/etc/flynn-controller/jsonschema/common.json",
				"schema/error.json":          "/etc/flynn-controller/jsonschema/error.json",
			},
			ExtraDirs: map[string]string{
				"schema/controller": "/etc/flynn-controller/jsonschema/controller",
				"schema/router":     "/etc/flynn-controller/jsonschema/router",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/start-flynn-controller"},
			},
		},
		{
			Name: "router",
			Base: "busybox",
			Binaries: map[string]string{
				"flynn-router": "/bin/flynn-router",
			},
			ExtraFiles: map[string]string{
				"util/ca-certs/ca-certs.pem": "/etc/ssl/certs/ca-certs.pem",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/flynn-router"},
			},
		},
		{
			Name: "logaggregator",
			Base: "busybox",
			Binaries: map[string]string{
				"logaggregator": "/bin/logaggregator",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/logaggregator"},
			},
		},
		{
			Name: "status",
			Base: "busybox",
			Binaries: map[string]string{
				"flynn-status": "/bin/flynn-status",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/flynn-status"},
			},
		},
		{
			Name: "updater",
			Base: "busybox",
			Binaries: map[string]string{
				"updater": "/bin/updater",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/updater"},
			},
		},

		// --- ubuntu-noble-based images ---
		{
			Name: "blobstore",
			Base: "ubuntu-noble",
			Binaries: map[string]string{
				"flynn-blobstore": "/bin/flynn-blobstore",
			},
			ExtraFiles: map[string]string{
				"util/ca-certs/ca-certs.pem": "/etc/ssl/certs/ca-certs.pem",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/flynn-blobstore", "server"},
			},
		},
		{
			Name: "host",
			Base: "ubuntu-noble",
			Binaries: map[string]string{
				"flynn-host": "/usr/local/bin/flynn-host",
				"flynn-init": "/usr/local/bin/flynn-init",
			},
			ExtraFiles: map[string]string{
				"util/ca-certs/ca-certs.pem": "/etc/ssl/certs/ca-certs.pem",
				"host/zfs-mknod.sh":          "/usr/local/bin/zfs-mknod",
				"host/udev.rules":            "/lib/udev/rules.d/10-local.rules",
				"host/start.sh":              "/usr/local/bin/start-flynn-host.sh",
				"host/cleanup.sh":            "/usr/local/bin/cleanup-flynn-host.sh",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/usr/local/bin/start-flynn-host.sh"},
			},
		},
		{
			Name: "postgres",
			Base: "ubuntu-noble",
			Binaries: map[string]string{
				"flynn-postgres":     "/bin/flynn-postgres",
				"flynn-postgres-api": "/bin/flynn-postgres-api",
			},
			ExtraFiles: map[string]string{
				"appliance/postgresql/start.sh": "/bin/start-flynn-postgres",
			},
			PackageScript: "appliance/postgresql/img/packages.sh",
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/start-flynn-postgres"},
			},
		},
		{
			Name: "redis",
			Base: "ubuntu-noble",
			Binaries: map[string]string{
				"flynn-redis":     "/bin/flynn-redis",
				"flynn-redis-api": "/bin/flynn-redis-api",
			},
			ExtraFiles: map[string]string{
				"appliance/redis/start.sh":   "/bin/start-flynn-redis",
				"appliance/redis/dump.sh":    "/bin/dump-flynn-redis",
				"appliance/redis/restore.sh": "/bin/restore-flynn-redis",
			},
			PackageScript: "appliance/redis/img/packages.sh",
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/start-flynn-redis"},
			},
		},
		{
			Name: "mariadb",
			Base: "ubuntu-noble",
			Binaries: map[string]string{
				"flynn-mariadb":     "/bin/flynn-mariadb",
				"flynn-mariadb-api": "/bin/flynn-mariadb-api",
			},
			ExtraFiles: map[string]string{
				"appliance/mariadb/start.sh": "/bin/start-flynn-mariadb",
			},
			PackageScript: "appliance/mariadb/img/packages.sh",
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/start-flynn-mariadb"},
			},
		},
		{
			Name: "mongodb",
			Base: "ubuntu-noble",
			Binaries: map[string]string{
				"flynn-mongodb":     "/bin/flynn-mongodb",
				"flynn-mongodb-api": "/bin/flynn-mongodb-api",
			},
			ExtraFiles: map[string]string{
				"appliance/mongodb/start.sh":   "/bin/start-flynn-mongodb",
				"appliance/mongodb/dump.sh":    "/bin/dump-flynn-mongodb",
				"appliance/mongodb/restore.sh": "/bin/restore-flynn-mongodb",
			},
			PackageScript: "appliance/mongodb/img/packages.sh",
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/start-flynn-mongodb"},
			},
		},
		{
			Name:          "gitreceive",
			Base:          "ubuntu-noble",
			PackageScript: "gitreceive/img/packages.sh",
			Binaries: map[string]string{
				"gitreceived":    "/bin/gitreceived",
				"flynn-receiver": "/bin/flynn-receiver",
			},
			ExtraFiles: map[string]string{
				"gitreceive/start.sh": "/bin/start-flynn-receiver",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/start-flynn-receiver"},
			},
		},
		{
			Name: "tarreceive",
			Base: "ubuntu-noble",
			Binaries: map[string]string{
				"tarreceive": "/bin/tarreceive",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/tarreceive"},
			},
		},
		{
			Name:          "taffy",
			Base:          "ubuntu-noble",
			PackageScript: "taffy/img/packages.sh",
			Binaries: map[string]string{
				"taffy":          "/bin/taffy",
				"flynn-receiver": "/bin/flynn-receiver",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/taffy"},
			},
		},
		{
			Name: "slugbuilder-24",
			Base: "ubuntu-noble",
			Binaries: map[string]string{
				"create-artifact": "/bin/create-artifact",
				"slug-migrator":   "/bin/slug-migrator",
			},
			ExtraFiles: map[string]string{
				"slugbuilder/convert-legacy-slug.sh": "/bin/convert-legacy-slug.sh",
				"slugbuilder/builder/build.sh":       "/builder/build.sh",
				"slugbuilder/builder/create-user.sh": "/builder/create-user.sh",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/builder/build.sh"},
			},
		},
		{
			Name: "slugbuilder-14",
			Base: "ubuntu-noble", // simplified: using noble instead of cedar-14/trusty
			Binaries: map[string]string{
				"create-artifact": "/bin/create-artifact",
				"slug-migrator":   "/bin/slug-migrator",
			},
			ExtraFiles: map[string]string{
				"slugbuilder/convert-legacy-slug.sh": "/bin/convert-legacy-slug.sh",
				"slugbuilder/builder/build.sh":       "/builder/build.sh",
				"slugbuilder/builder/create-user.sh": "/builder/create-user.sh",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/builder/build.sh"},
			},
		},
		{
			Name: "slugrunner-24",
			Base: "ubuntu-noble",
			ExtraFiles: map[string]string{
				"slugrunner/runner/init": "/runner/init",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/runner/init"},
			},
		},
		{
			Name: "slugrunner-14",
			Base: "ubuntu-noble", // simplified
			ExtraFiles: map[string]string{
				"slugrunner/runner/init": "/runner/init",
			},
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/runner/init"},
			},
		},
		{
			Name: "builder",
			Base: "ubuntu-noble",
			Binaries: map[string]string{
				"flynn-builder": "/bin/flynn-builder",
			},
		},
	}
}

// ----- Step 3: Generate manifests -----

func (e *exporter) generateManifests() error {
	manifestsDir := filepath.Join(e.buildDir, "manifests")
	if err := os.MkdirAll(manifestsDir, 0755); err != nil {
		return err
	}

	// Generate images.json
	fmt.Printf("  Generating images.json\n")
	imagesJSON, err := e.generateImagesJSON()
	if err != nil {
		return fmt.Errorf("generating images.json: %s", err)
	}
	if err := os.WriteFile(filepath.Join(manifestsDir, "images.json"), imagesJSON, 0644); err != nil {
		return err
	}

	// Generate bootstrap-manifest.json
	fmt.Printf("  Generating bootstrap-manifest.json\n")
	bootstrapManifest, err := e.generateBootstrapManifest()
	if err != nil {
		return fmt.Errorf("generating bootstrap-manifest.json: %s", err)
	}
	if err := os.WriteFile(filepath.Join(manifestsDir, "bootstrap-manifest.json"), bootstrapManifest, 0644); err != nil {
		return err
	}

	return nil
}

func (e *exporter) generateImagesJSON() ([]byte, error) {
	// Read the template
	templatePath := filepath.Join(e.sourceDir, "util/release/images_template.json")
	template, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, err
	}

	// Replace $image_artifact[name] placeholders with actual artifact JSON
	pattern := regexp.MustCompile(`\$image_artifact\[([^\]]+)\]`)
	var replaceErr error
	result := pattern.ReplaceAllFunc(template, func(match []byte) []byte {
		name := string(match[16 : len(match)-1])
		artifact, ok := e.artifacts[name]
		if !ok {
			replaceErr = fmt.Errorf("unknown image %q", name)
			return nil
		}
		// Set meta for the artifact in the manifest
		artifact.Meta = map[string]string{
			"flynn.component":    name,
			"flynn.system-image": "true",
		}
		data, err := json.Marshal(artifact)
		if err != nil {
			replaceErr = err
			return nil
		}
		return data
	})
	if replaceErr != nil {
		return nil, replaceErr
	}

	// Validate the result is valid JSON
	var check interface{}
	if err := json.Unmarshal(result, &check); err != nil {
		return nil, fmt.Errorf("generated images.json is not valid JSON: %s", err)
	}

	return result, nil
}

func (e *exporter) generateBootstrapManifest() ([]byte, error) {
	templatePath := filepath.Join(e.sourceDir, "bootstrap/manifest_template.json")
	template, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, err
	}

	// Replace $image_artifact[name] placeholders
	pattern := regexp.MustCompile(`\$image_artifact\[([^\]]+)\]`)
	var replaceErr error
	result := pattern.ReplaceAllFunc(template, func(match []byte) []byte {
		name := string(match[16 : len(match)-1])
		artifact, ok := e.artifacts[name]
		if !ok {
			replaceErr = fmt.Errorf("unknown image %q in bootstrap manifest", name)
			return nil
		}
		artifact.Meta = map[string]string{
			"flynn.component":    name,
			"flynn.system-image": "true",
		}
		data, err := json.Marshal(artifact)
		if err != nil {
			replaceErr = err
			return nil
		}
		return data
	})
	if replaceErr != nil {
		return nil, replaceErr
	}

	return result, nil
}

// ----- Step 4: Stage TUF targets -----

func (e *exporter) stageTUFTargets() error {
	// Open the TUF repository
	store := tuf.FileSystemStore(e.tufDir, func(role string, confirm bool) ([]byte, error) {
		// Keys are unencrypted, return empty passphrase
		return []byte(""), nil
	})
	repo, err := tuf.NewRepo(store)
	if err != nil {
		return fmt.Errorf("opening TUF repo: %s", err)
	}

	// Get existing targets (may be empty)
	existingTargets, err := repo.Targets()
	if err != nil {
		return fmt.Errorf("getting existing targets: %s", err)
	}
	_ = existingTargets

	// Clean staged area
	if err := repo.Clean(); err != nil {
		return fmt.Errorf("cleaning TUF repo: %s", err)
	}

	targetMeta, _ := json.Marshal(map[string]string{"version": e.version})

	stagedTargetsDir := filepath.Join(e.tufDir, "staged", "targets")

	// 4a: Stage versioned binaries (gzipped)
	fmt.Printf("  Staging versioned binaries\n")
	for _, bin := range []string{"flynn-host", "flynn-init", "flynn-linux-amd64"} {
		target := filepath.Join(e.version, bin+".gz")
		srcPath := filepath.Join(e.binDir, bin)
		if err := e.stageGzipped(stagedTargetsDir, target, srcPath); err != nil {
			return fmt.Errorf("staging %s: %s", bin, err)
		}
		if err := repo.AddTarget(util.NormalizeTarget(target), targetMeta); err != nil {
			return fmt.Errorf("adding target %s: %s", target, err)
		}
		fmt.Printf("    + %s\n", target)
	}

	// 4b: Stage top-level flynn-host binary (for install script)
	fmt.Printf("  Staging top-level binaries\n")
	{
		target := "flynn-host.gz"
		srcPath := filepath.Join(e.binDir, "flynn-host")
		if err := e.stageGzipped(stagedTargetsDir, target, srcPath); err != nil {
			return fmt.Errorf("staging top-level flynn-host: %s", err)
		}
		if err := repo.AddTarget(util.NormalizeTarget(target), targetMeta); err != nil {
			return fmt.Errorf("adding target %s: %s", target, err)
		}
		fmt.Printf("    + %s\n", target)
	}

	// Top-level CLI binary
	{
		target := "flynn-linux-amd64.gz"
		srcPath := filepath.Join(e.binDir, "flynn-linux-amd64")
		if err := e.stageGzipped(stagedTargetsDir, target, srcPath); err != nil {
			return fmt.Errorf("staging top-level CLI: %s", err)
		}
		if err := repo.AddTarget(util.NormalizeTarget(target), targetMeta); err != nil {
			return fmt.Errorf("adding target %s: %s", target, err)
		}
		fmt.Printf("    + %s\n", target)
	}

	// 4c: Stage versioned manifests (gzipped)
	fmt.Printf("  Staging versioned manifests\n")
	manifestsDir := filepath.Join(e.buildDir, "manifests")
	for _, manifest := range []string{"bootstrap-manifest.json", "images.json"} {
		target := filepath.Join(e.version, manifest+".gz")
		srcPath := filepath.Join(manifestsDir, manifest)

		// Read the manifest and rewrite layer URLs
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("reading %s: %s", manifest, err)
		}

		if err := e.stageGzippedData(stagedTargetsDir, target, data); err != nil {
			return fmt.Errorf("staging %s: %s", manifest, err)
		}
		if err := repo.AddTarget(util.NormalizeTarget(target), targetMeta); err != nil {
			return fmt.Errorf("adding target %s: %s", target, err)
		}
		fmt.Printf("    + %s\n", target)
	}

	// 4d: Stage image manifests and layers
	fmt.Printf("  Staging images and layers\n")
	layersStaged := make(map[string]bool) // track deduplicated layers
	for name, artifact := range e.artifacts {
		manifestID := artifact.Manifest().ID()
		imageTarget := util.NormalizeTarget(path.Join("images", manifestID+".json"))

		// Stage image manifest
		imagePath := filepath.Join(stagedTargetsDir, imageTarget)
		if err := os.MkdirAll(filepath.Dir(imagePath), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(imagePath, artifact.RawManifest, 0644); err != nil {
			return err
		}
		if err := repo.AddTarget(imageTarget, targetMeta); err != nil {
			return fmt.Errorf("adding image target %s: %s", name, err)
		}
		fmt.Printf("    + images/%s.json (%s)\n", manifestID[:16], name)

		// Stage layers
		for _, rootfs := range artifact.Manifest().Rootfs {
			for _, layer := range rootfs.Layers {
				if layersStaged[layer.ID] {
					continue
				}
				layersStaged[layer.ID] = true

				// Stage squashfs layer
				layerTarget := util.NormalizeTarget(path.Join("layers", layer.ID+".squashfs"))
				layerSrc := filepath.Join(e.layerCache, layer.ID+".squashfs")
				layerDst := filepath.Join(stagedTargetsDir, layerTarget)
				if err := os.MkdirAll(filepath.Dir(layerDst), 0755); err != nil {
					return err
				}
				if err := copyFile(layerSrc, layerDst, 0644); err != nil {
					return fmt.Errorf("staging layer %s: %s", layer.ID[:16], err)
				}
				if err := repo.AddTarget(layerTarget, targetMeta); err != nil {
					return fmt.Errorf("adding layer target %s: %s", layer.ID[:16], err)
				}

				// Stage layer config JSON
				layerConfigTarget := util.NormalizeTarget(path.Join("layers", layer.ID+".json"))
				layerConfigData, err := json.Marshal(layer)
				if err != nil {
					return err
				}
				layerConfigDst := filepath.Join(stagedTargetsDir, layerConfigTarget)
				if err := os.WriteFile(layerConfigDst, layerConfigData, 0644); err != nil {
					return err
				}
				if err := repo.AddTarget(layerConfigTarget, targetMeta); err != nil {
					return fmt.Errorf("adding layer config target %s: %s", layer.ID[:16], err)
				}

				fmt.Printf("    + layers/%s.squashfs (%s)\n", layer.ID[:16], humanSize(layer.Length))
			}
		}
	}

	// 4e: Stage channel file
	fmt.Printf("  Staging channel file\n")
	channelTarget := util.NormalizeTarget(path.Join("channels", "stable"))
	channelPath := filepath.Join(stagedTargetsDir, channelTarget)
	if err := os.MkdirAll(filepath.Dir(channelPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(channelPath, []byte(e.version+"\n"), 0644); err != nil {
		return err
	}
	if err := repo.AddTarget(channelTarget, targetMeta); err != nil {
		return fmt.Errorf("adding channel target: %s", err)
	}
	fmt.Printf("    + channels/stable -> %s\n", e.version)

	// 4f: Sign and commit
	fmt.Printf("  Signing TUF metadata\n")
	if err := repo.Snapshot(tuf.CompressionTypeNone); err != nil {
		return fmt.Errorf("TUF snapshot: %s", err)
	}
	if err := repo.Timestamp(); err != nil {
		return fmt.Errorf("TUF timestamp: %s", err)
	}
	if err := repo.Commit(); err != nil {
		return fmt.Errorf("TUF commit: %s", err)
	}
	fmt.Printf("  TUF metadata signed and committed\n")

	return nil
}

func (e *exporter) stageGzipped(stagedTargetsDir, target, srcPath string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	return e.stageGzippedData(stagedTargetsDir, target, data)
}

func (e *exporter) stageGzippedData(stagedTargetsDir, target string, data []byte) error {
	dstPath := filepath.Join(stagedTargetsDir, target)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}
	f, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return err
	}
	if _, err := io.Copy(gz, bytes.NewReader(data)); err != nil {
		gz.Close()
		return err
	}
	return gz.Close()
}

// ----- Utilities -----

func copyFile(src, dst string, perm os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, perm)
}

func humanSize(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
	)
	switch {
	case bytes >= MB:
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1fKB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// Ensure these imports are used
var (
	_ = tufdata.Files{}
	_ = sha512.Sum512_256
	_ = hex.EncodeToString
	_ = path.Join
)
