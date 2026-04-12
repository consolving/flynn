// rotate-tuf-keys performs a complete TUF key rotation.
//
// It generates new ed25519 keys for all 4 TUF roles (root, targets, snapshot,
// timestamp), revokes the old keys, re-signs all metadata, and moves the new
// private keys to a secure location outside the repository.
//
// The rotation follows the TUF specification: the new root.json is signed by
// both old and new root keys so that existing clients can verify the transition.
//
// Usage:
//
//	go run ./script/rotate-tuf-keys \
//	    --tuf-dir=/path/to/flynn-tuf-repo \
//	    --keys-out=/path/to/secure/keys
//
// The --keys-out directory will contain the new private keys and must be kept
// secure (outside any git repository).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tuf "github.com/flynn/go-tuf"
	"github.com/flynn/go-tuf/data"
)

func main() {
	tufDir := flag.String("tuf-dir", "", "Path to the TUF repository directory (containing keys/ and repository/)")
	keysOut := flag.String("keys-out", "", "Path to store new private keys (outside the repo)")
	flag.Parse()

	if *tufDir == "" || *keysOut == "" {
		fmt.Fprintf(os.Stderr, "Usage: rotate-tuf-keys --tuf-dir=DIR --keys-out=DIR\n")
		os.Exit(1)
	}

	if err := run(*tufDir, *keysOut); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func run(tufDir, keysOut string) error {
	// Verify the TUF repo exists
	rootPath := filepath.Join(tufDir, "repository", "root.json")
	if _, err := os.Stat(rootPath); err != nil {
		return fmt.Errorf("TUF repository not found at %s: %s", tufDir, err)
	}

	// Create the output directory for new keys
	if err := os.MkdirAll(keysOut, 0700); err != nil {
		return fmt.Errorf("creating keys output directory: %s", err)
	}

	// Open the TUF repository (keys are unencrypted, no passphrase needed)
	store := tuf.FileSystemStore(tufDir, nil)
	repo, err := tuf.NewRepo(store)
	if err != nil {
		return fmt.Errorf("opening TUF repo: %s", err)
	}

	// Step 1: Read current root metadata to get old key IDs
	fmt.Println("=== Step 1: Reading current root metadata ===")
	currentRootData, err := os.ReadFile(rootPath)
	if err != nil {
		return fmt.Errorf("reading root.json: %s", err)
	}
	var currentSigned data.Signed
	if err := json.Unmarshal(currentRootData, &currentSigned); err != nil {
		return fmt.Errorf("parsing root.json: %s", err)
	}
	var currentRoot data.Root
	if err := json.Unmarshal(currentSigned.Signed, &currentRoot); err != nil {
		return fmt.Errorf("parsing root.json signed data: %s", err)
	}

	fmt.Printf("  Current root.json version: %d\n", currentRoot.Version)
	fmt.Printf("  Current root.json expires: %s\n", currentRoot.Expires.Format(time.RFC3339))

	// Collect old key IDs for each role
	oldKeyIDs := make(map[string][]string)
	for role, roleData := range currentRoot.Roles {
		oldKeyIDs[role] = make([]string, len(roleData.KeyIDs))
		copy(oldKeyIDs[role], roleData.KeyIDs)
		fmt.Printf("  Role %s: %d key(s), threshold=%d\n", role, len(roleData.KeyIDs), roleData.Threshold)
		for _, kid := range roleData.KeyIDs {
			fmt.Printf("    old key: %s\n", kid)
		}
	}

	// Step 2: Generate new keys for all roles
	// Order matters: root first, then the others.
	// GenKey adds the new key alongside the old one and bumps root.json version.
	fmt.Println("\n=== Step 2: Generating new keys ===")
	roles := []string{"root", "targets", "snapshot", "timestamp"}
	newKeyIDs := make(map[string]string)

	for _, role := range roles {
		expires := data.DefaultExpires("root") // All GenKey calls update root.json expiry
		newID, err := repo.GenKeyWithExpires(role, expires)
		if err != nil {
			return fmt.Errorf("generating new %s key: %s", role, err)
		}
		newKeyIDs[role] = newID
		fmt.Printf("  Generated new %s key: %s\n", role, newID)
	}

	// Step 3: Revoke old keys for all roles
	// For root: go-tuf automatically signs with both old and new keys
	fmt.Println("\n=== Step 3: Revoking old keys ===")
	for _, role := range roles {
		for _, oldID := range oldKeyIDs[role] {
			fmt.Printf("  Revoking %s key: %s\n", role, oldID)
			if err := repo.RevokeKey(role, oldID); err != nil {
				return fmt.Errorf("revoking %s key %s: %s", role, oldID, err)
			}
		}
	}

	// Step 4: Re-sign targets, snapshot, timestamp metadata
	// The targets metadata needs to be re-signed with the new targets key.
	// Snapshot and timestamp are always re-generated.
	fmt.Println("\n=== Step 4: Re-signing metadata ===")

	// Re-sign targets.json with the new targets key
	fmt.Println("  Re-signing targets.json...")
	if err := repo.Sign("targets.json"); err != nil {
		return fmt.Errorf("re-signing targets.json: %s", err)
	}

	// Re-generate snapshot (includes hashes of root.json and targets.json)
	fmt.Println("  Re-generating snapshot.json...")
	if err := repo.Snapshot(tuf.CompressionTypeNone); err != nil {
		return fmt.Errorf("generating snapshot: %s", err)
	}

	// Re-generate timestamp (includes hash of snapshot.json)
	fmt.Println("  Re-generating timestamp.json...")
	if err := repo.Timestamp(); err != nil {
		return fmt.Errorf("generating timestamp: %s", err)
	}

	// Commit: validates all signatures and copies staged -> repository
	fmt.Println("  Committing...")
	if err := repo.Commit(); err != nil {
		return fmt.Errorf("committing TUF metadata: %s", err)
	}

	// Step 5: Copy new private keys to secure location outside the repo
	fmt.Printf("\n=== Step 5: Copying keys to %s ===\n", keysOut)
	keysDir := filepath.Join(tufDir, "keys")
	for _, role := range roles {
		src := filepath.Join(keysDir, role+".json")
		dst := filepath.Join(keysOut, role+".json")
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("reading key file %s: %s", src, err)
		}
		if err := os.WriteFile(dst, data, 0600); err != nil {
			return fmt.Errorf("writing key file %s: %s", dst, err)
		}
		fmt.Printf("  Copied %s.json (%d bytes)\n", role, len(data))
	}

	// Step 6: Remove old private keys from the TUF repo directory
	fmt.Println("\n=== Step 6: Removing keys from TUF repo ===")
	for _, role := range roles {
		keyFile := filepath.Join(keysDir, role+".json")
		if err := os.Remove(keyFile); err != nil {
			return fmt.Errorf("removing key file %s: %s", keyFile, err)
		}
		fmt.Printf("  Removed %s\n", keyFile)
	}
	// Remove the keys directory itself if empty
	if err := os.Remove(keysDir); err != nil {
		fmt.Printf("  Warning: could not remove keys/ directory: %s\n", err)
	} else {
		fmt.Println("  Removed keys/ directory")
	}

	// Step 7: Print summary with new public keys for configuration updates
	fmt.Println("\n=== Step 7: Summary ===")

	// Read the final root.json to get the new public keys
	finalRootData, err := os.ReadFile(filepath.Join(tufDir, "repository", "root.json"))
	if err != nil {
		return fmt.Errorf("reading final root.json: %s", err)
	}
	var finalSigned data.Signed
	if err := json.Unmarshal(finalRootData, &finalSigned); err != nil {
		return fmt.Errorf("parsing final root.json: %s", err)
	}
	var finalRoot data.Root
	if err := json.Unmarshal(finalSigned.Signed, &finalRoot); err != nil {
		return fmt.Errorf("parsing final root.json signed data: %s", err)
	}

	fmt.Printf("\n  New root.json version: %d\n", finalRoot.Version)
	fmt.Printf("  New root.json expires: %s\n", finalRoot.Expires.Format(time.RFC3339))
	fmt.Printf("  Consistent snapshots: %v\n", finalRoot.ConsistentSnapshot)
	fmt.Println()

	// Print new root public keys in the format needed for configuration
	rootRole := finalRoot.Roles["root"]
	fmt.Println("  New root public keys (for tup.config, builder/manifest.json, tufconfig.go):")
	fmt.Println()

	// Build the JSON array for CONFIG_TUF_ROOT_KEYS
	var rootKeys []map[string]interface{}
	for _, kid := range rootRole.KeyIDs {
		key := finalRoot.Keys[kid]
		rootKeys = append(rootKeys, map[string]interface{}{
			"keytype": key.Type,
			"keyval": map[string]string{
				"public": fmt.Sprintf("%x", []byte(key.Value.Public)),
			},
		})
	}
	rootKeysJSON, err := json.MarshalIndent(rootKeys, "  ", "  ")
	if err != nil {
		return fmt.Errorf("marshaling root keys: %s", err)
	}
	fmt.Printf("  %s\n", rootKeysJSON)

	fmt.Println()
	fmt.Println("  Key rotation complete!")
	fmt.Println()
	fmt.Println("  IMPORTANT: You must now update the following files with the new root public keys:")
	fmt.Println("    1. flynn/tup.config           (CONFIG_TUF_ROOT_KEYS)")
	fmt.Println("    2. flynn/builder/manifest.json (tuf.root_keys)")
	fmt.Println("    3. flynn/pkg/tufconfig/tufconfig.go (RootKeysJSON)")
	fmt.Println()
	fmt.Printf("  Private keys are stored at: %s\n", keysOut)
	fmt.Println("  Keep this directory secure and NEVER commit it to git!")

	return nil
}
