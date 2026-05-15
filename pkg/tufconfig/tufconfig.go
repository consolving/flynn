package tufconfig

import (
	"encoding/json"

	"github.com/flynn/go-tuf/data"
)

var (
	// these constants are overridden at build time (see builder/go-wrapper.sh)
	RootKeysJSON = `[{"keytype":"ed25519","keyval":{"public":"cddd70123e8303002498fc7f9f8c1fff87cdb321444c67b1ba9190d0394f6134"}},{"keytype":"ed25519","keyval":{"public":"22f67c648aaade626bbd8a85aac1e02d77cb476488a967b1ece129c701ed314c"}},{"keytype":"ed25519","keyval":{"public":"29e3309c3ed70d4927b2f55adc7ac5f5d547731fb62c5f197c02d0c1c2abac21"}},{"keytype":"ed25519","keyval":{"public":"d77ef5acdccc6ffba650edd4bc4d292014e7afbd1f3d5af945395e587c1430b1"}}]`
	Repository   = "https://consolving.github.io/flynn-tuf-repo/repository"

	// Mirrors is an ordered list of TUF repository URLs to try.
	// On network/timeout errors, the next mirror is attempted.
	// TUF signature verification failures are NOT retried (they indicate
	// tampering, not a transient issue).
	Mirrors = []string{
		"https://consolving.github.io/flynn-tuf-repo/repository", // GitHub Pages (current primary)
		// Future: add IPFS-backed gateway as primary, e.g.:
		// "https://tuf.consolving.net/repository",
	}
)

var RootKeys []*data.Key

func init() {
	if err := json.Unmarshal([]byte(RootKeysJSON), &RootKeys); err != nil {
		panic("error decoding root keys")
	}
}
