package tufconfig

import (
	"encoding/json"

	"github.com/flynn/go-tuf/data"
)

var (
	// these constants are overridden at build time (see builder/go-wrapper.sh)
	RootKeysJSON = `[{"keytype":"ed25519","keyval":{"public":"8f6ac68cbbe1108154b81793bc39e603bfbccfb7c4d92a159b7e94108f4cf7da"}},{"keytype":"ed25519","keyval":{"public":"3c659ab84ccd2b48802b41a45ad1799b06b0f87998583111ed61ee63ac694df8"}},{"keytype":"ed25519","keyval":{"public":"e81c1b222040f6580749060c6b52f8f3859b3a1acb0551bbc70aabecf8ea8922"}},{"keytype":"ed25519","keyval":{"public":"a7e5ff94fbe4167c272bb0a105cd56bb0bc5da6d6b90b8a36fdbc8714a64cd79"}}]`
	Repository   = "https://consolving.github.io/flynn-tuf-repo/repository"
)

var RootKeys []*data.Key

func init() {
	if err := json.Unmarshal([]byte(RootKeysJSON), &RootKeys); err != nil {
		panic("error decoding root keys")
	}
}
