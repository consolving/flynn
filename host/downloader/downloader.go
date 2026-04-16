package downloader

import (
	"compress/gzip"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"syscall"

	ct "github.com/flynn/flynn/controller/types"
	"github.com/flynn/flynn/host/volume"
	"github.com/flynn/flynn/host/volume/manager"
	"github.com/flynn/flynn/pkg/tufutil"
	tuf "github.com/flynn/go-tuf/client"
)

var binaries = []string{
	"flynn-host",
	"flynn-linux-amd64",
	"flynn-init",
}

var config = []string{
	"bootstrap-manifest.json",
}

// Downloader downloads versioned files using a tuf client
type Downloader struct {
	client  *tuf.Client
	vman    *volumemanager.Manager
	version string
}

func New(client *tuf.Client, vman *volumemanager.Manager, version string) *Downloader {
	return &Downloader{client, vman, version}
}

// DownloadBinaries downloads the Flynn binaries using the tuf client to the
// given dir with the version suffixed (e.g. /usr/local/bin/flynn-host.v20150726.0)
// and updates non-versioned symlinks.
func (d *Downloader) DownloadBinaries(dir string) (map[string]string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("error creating bin dir: %s", err)
	}
	paths := make(map[string]string, len(binaries))
	for _, bin := range binaries {
		path, err := d.downloadGzippedFile(bin, dir, true)
		if err != nil {
			return nil, err
		}
		if err := os.Chmod(path, 0755); err != nil {
			return nil, err
		}
		paths[bin] = path
	}
	// symlink flynn to flynn-linux-amd64
	if err := symlink("flynn-linux-amd64", filepath.Join(dir, "flynn")); err != nil {
		return nil, err
	}
	return paths, nil
}

// DownloadConfig downloads the Flynn config files using the tuf client to the
// given dir.
func (d *Downloader) DownloadConfig(dir string) (map[string]string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("error creating config dir: %s", err)
	}
	paths := make(map[string]string, len(config))
	for _, conf := range config {
		path, err := d.downloadGzippedFile(conf, dir, false)
		if err != nil {
			return nil, err
		}
		paths[conf] = path
	}
	return paths, nil
}

func (d *Downloader) DownloadImages(dir string, info chan *ct.ImagePullInfo) error {
	defer close(info)

	path := filepath.Join(d.version, "images.json.gz")
	tmp, err := tufutil.Download(d.client, path)
	if err != nil {
		return err
	}
	defer tmp.Close()

	gz, err := gzip.NewReader(tmp)
	if err != nil {
		return err
	}
	defer gz.Close()

	out, err := os.Create(filepath.Join(dir, "images."+d.version+".json"))
	if err != nil {
		return err
	}
	defer out.Close()

	var images map[string]*ct.Artifact
	if err := json.NewDecoder(io.TeeReader(gz, out)).Decode(&images); err != nil {
		return err
	}

	for _, image := range images {
		if err := d.downloadImage(image, info); err != nil {
			return err
		}
	}

	return nil
}

func (d *Downloader) downloadImage(artifact *ct.Artifact, info chan *ct.ImagePullInfo) error {
	info <- &ct.ImagePullInfo{
		Name:     artifact.Meta["flynn.component"],
		Type:     ct.ImagePullTypeImage,
		Artifact: artifact,
	}

	for _, rootfs := range artifact.Manifest().Rootfs {
		for _, layer := range rootfs.Layers {
			if layer.Type != ct.ImageLayerTypeSquashfs {
				continue
			}

			info <- &ct.ImagePullInfo{
				Name:  artifact.Meta["flynn.component"],
				Type:  ct.ImagePullTypeLayer,
				Layer: layer,
			}

			if err := d.downloadSquashfsLayer(layer, artifact.LayerURL(layer), artifact.Meta); err != nil {
				return fmt.Errorf("error downloading layer: %s", err)
			}
		}
	}

	return nil
}

func (d *Downloader) downloadSquashfsLayer(layer *ct.ImageLayer, layerURL string, meta map[string]string) error {
	if vol := d.vman.GetVolume(layer.ID); vol != nil {
		return nil
	}

	// Download the layer directly from the URL (e.g. GitHub Releases).
	// The original code used the TUF client for verified downloads, but
	// our layer files are hosted on GitHub Releases (a different host
	// from the TUF metadata on GitHub Pages), so the TUF client can't
	// fetch them. Instead, download directly and verify the SHA256 hash
	// against the layer ID (which IS the content hash).
	tmp, err := d.downloadAndVerify(layerURL, layer.ID, layer.Length)
	if err != nil {
		return err
	}
	defer tmp.Close()

	_, err = d.vman.ImportFilesystem("default", &volume.Filesystem{
		ID:         layer.ID,
		Data:       tmp,
		Size:       layer.Length,
		Type:       volume.VolumeTypeSquashfs,
		MountFlags: syscall.MS_RDONLY,
		Meta:       meta,
	})
	return err
}

// downloadAndVerify fetches a URL to a temp file, verifying the SHA256 hash
// and expected size. The returned file is seeked to the start.
func (d *Downloader) downloadAndVerify(url, expectedHash string, expectedSize int64) (*tufutil.TempFile, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("error fetching %s: %s", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d fetching %s", resp.StatusCode, url)
	}

	tmp, err := tufutil.NewTempFile()
	if err != nil {
		return nil, err
	}

	h := sha512.New512_256()
	n, err := io.Copy(tmp, io.TeeReader(resp.Body, h))
	if err != nil {
		tmp.Close()
		return nil, fmt.Errorf("error downloading %s: %s", url, err)
	}

	if expectedSize > 0 && n != expectedSize {
		tmp.Close()
		return nil, fmt.Errorf("size mismatch for %s: expected %d, got %d", url, expectedSize, n)
	}

	actualHash := hex.EncodeToString(h.Sum(nil))
	if actualHash != expectedHash {
		tmp.Close()
		return nil, fmt.Errorf("hash mismatch for %s: expected %s, got %s", url, expectedHash, actualHash)
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		tmp.Close()
		return nil, err
	}
	return tmp, nil
}

func (d *Downloader) downloadGzippedFile(name, dir string, versionSuffix bool) (string, error) {
	path := path.Join(d.version, name)
	gzPath := path + ".gz"
	dst := filepath.Join(dir, name)
	if versionSuffix {
		dst = dst + "." + d.version
	}

	file, err := tufutil.Download(d.client, gzPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// unlink the destination file in case it's in use
	os.Remove(dst)

	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	_, err = io.Copy(out, gz)
	if err != nil {
		return "", err
	}

	if versionSuffix {
		// symlink the non-versioned path to the versioned path
		// e.g. flynn-host -> flynn-host.v20150726.0
		link := filepath.Join(dir, name)
		if err := symlink(filepath.Base(dst), link); err != nil {
			return "", err
		}
	}

	return dst, nil
}

func symlink(oldname, newname string) error {
	os.Remove(newname)
	return os.Symlink(oldname, newname)
}
