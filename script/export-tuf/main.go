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
	Name       string
	Base       string            // base image name for layer inheritance
	Binaries   map[string]string // source binary name -> dest path in squashfs
	ExtraFiles map[string]string // source file (relative to source-dir) -> dest path
	Entrypoint *ct.ImageEntrypoint
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		tufDir     string
		buildDir   string
		sourceDir  string
		version    string
		layerCache string
		tufRepo    string
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
		tufDir:      tufDir,
		buildDir:    buildDir,
		binDir:      binDir,
		sourceDir:   sourceDir,
		version:     version,
		layerCache:  layerCache,
		tufRepoURL:  tufRepo,
		layerURLTpl: fmt.Sprintf("%s?target=/layers/{id}.squashfs", tufRepo),
		baseLayers:  make(map[string][]*ct.ImageLayer),
		artifacts:   make(map[string]*ct.Artifact),
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
`)
}

type exporter struct {
	tufDir      string
	buildDir    string
	binDir      string
	sourceDir   string
	version     string
	layerCache  string
	tufRepoURL  string
	layerURLTpl string

	baseLayers map[string][]*ct.ImageLayer // base image name -> accumulated layers
	artifacts  map[string]*ct.Artifact     // image name -> artifact
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
	// Each base layer becomes a squashfs file in the layer cache.
	//
	// Dependency tree:
	//   busybox (standalone)
	//   ubuntu-noble (standalone)
	//   ubuntu-noble (standalone, needed only for host image)

	bases := []struct {
		name   string
		script string
	}{
		{"busybox", "builder/img/busybox.sh"},
		{"ubuntu-noble", "builder/img/ubuntu-noble.sh"},
		// ubuntu-noble needed for host image but host image also needs
		// kernel packages which require a full apt - skip for now as
		// the host image will use ubuntu-noble in the simplified pipeline
	}

	for _, base := range bases {
		fmt.Printf("  Building base layer: %s\n", base.name)
		layer, err := e.buildBaseLayer(base.name, base.script)
		if err != nil {
			return fmt.Errorf("building %s: %s", base.name, err)
		}
		e.baseLayers[base.name] = []*ct.ImageLayer{layer}
		fmt.Printf("  -> %s: id=%s size=%d\n", base.name, layer.ID, layer.Length)
	}

	return nil
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

	// Copy binaries
	for srcName, destPath := range spec.Binaries {
		srcPath := filepath.Join(e.binDir, srcName)
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
		dst := filepath.Join(tmpDir, destPath)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		if err := copyFile(srcPath, dst, 0755); err != nil {
			return fmt.Errorf("copying extra file %s: %s", srcRel, err)
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

	// Build the ImageManifest with base layers + component layer
	var allLayers []*ct.ImageLayer
	if baseLayers, ok := e.baseLayers[spec.Base]; ok {
		allLayers = append(allLayers, baseLayers...)
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
			Entrypoint: &ct.ImageEntrypoint{
				Args: []string{"/bin/start-flynn-mongodb"},
			},
		},
		{
			Name: "gitreceive",
			Base: "ubuntu-noble",
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
			Name: "taffy",
			Base: "ubuntu-noble",
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
