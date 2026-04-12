# Step-by-Step: Creating a Public Flynn TUF Repository

This is a complete walkthrough from start to finish.

---

## Prerequisites

```bash
# Basic tools
which curl jq sha512sum python3 go

# Install missing tools
sudo apt-get update
sudo apt-get install -y curl jq python3

# Verify Go installation
go version
# Expected: go version go1.13+
```

---

## Phase 1: Create TUF Repository (Local)

### Step 1: Create Repository Directory

```bash
# Create working directory
mkdir -p ~/flynn-tuf-setup
cd ~/flynn-tuf-setup

# Create local TUF repository structure
mkdir -p flynn-tuf-repo
cd flynn-tuf-repo
```

### Step 2: Initialize TUF

```bash
# Install TUF CLI
go install github.com/flynn/go-tuf/cmd/tuf@latest

# Add to PATH if needed
export PATH=$PATH:$(go env GOPATH)/bin

# Initialize repository
tuf init
```

**Expected output**:
```
Initialized TUF repository in /path/to/flynn-tuf-repo
```

### Step 3: Generate Signing Keys

You'll be prompted for passphrases **4 times**. Use strong passwords and **write them down securely**.

```bash
# Generate root key
tuf gen-key root
# Enter passphrase: your-root-passphrase
# Repeat passphrase: your-root-passphrase

# Generate targets key
tuf gen-key targets
# Enter passphrase: your-targets-passphrase
# Repeat passphrase: your-targets-passphrase

# Generate snapshot key
tuf gen-key snapshot
# Enter passphrase: your-snapshot-passphrase  
# Repeat passphrase: your-snapshot-passphrase

# Generate timestamp key
tuf gen-key timestamp
# Enter passphrase: your-timestamp-passphrase
# Repeat passphrase: your-timestamp-passphrase
```

**After this, your directory structure should look like**:
```
flynn-tuf-repo/
├── keys/
│   ├── root.json
│   ├── snapshot.json
│   ├── targets.json
│   └── timestamp.json
├── repository/
└── staged/
    ├── root.json
    └── targets/
```

### Step 4: Sign Root Manifest

```bash
# Sign the root manifest
tuf sign root
# Enter targets keys passphrase: your-targets-passphrase
```

### Step 5: Commit Changes

```bash
# Commit all staged changes
tuf commit
```

**Final structure**:
```
flynn-tuf-repo/
├── keys/
│   ├── root.json
│   ├── snapshot.json
│   ├── targets.json
│   └── timestamp.json
├── repository/
│   ├── root.json
│   ├── snapshot.json
│   ├── targets/
│   ├── targets.json
│   └── timestamp.json
└── staged/
```

### Step 6: Get Root Keys for Flynn Config

```bash
# Get the root keys (public keys only)
tuf root-keys

# Save this output - you'll need it in the next step
# Example output:
# [{"keytype":"ed25519","keyval":{"public":"6cfda23aa48f530aebd5b9c01030d06d02f25876b5508d681675270027af4731"}}]
```

---

## Phase 2: Deploy to Public Server (GitHub Pages)

### Step 7: Create GitHub Repository

1. Go to https://github.com/new
2. Repository name: `flynn-tuf-repo`
3. Public
4. Initialize with README: ✓
5. Create repository

### Step 8: Push TUF Repository to GitHub

```bash
# Go back to your TUF repo
cd ~/flynn-tuf-setup/flynn-tuf-repo

# Initialize git (if not already done)
git init

# Copy the repository directory to root (GitHub Pages expects this)
cp -r repository/* .
cp -r keys/ .

# Add files
git add .
git commit -m "Initial Flynn TUF repository"
git branch -m main
git remote add origin https://github.com/YOUR_USERNAME/flynn-tuf-repo.git
git push -u origin main
```

### Step 9: Enable GitHub Pages

1. Go to: https://github.com/YOUR_USERNAME/flynn-tuf-repo/settings/pages
2. Source: Deploy from a branch
3. Branch: main
4. Folder: / (root)
5. Save

Your TUF repository is now available at:
```
https://YOUR_USERNAME.github.io/flynn-tuf-repo/
```

### Step 10: Verify Repository is Accessible

```bash
# Test root.json
curl https://YOUR_USERNAME.github.io/flynn-tuf-repo/root.json | jq .

# Test targets.json
curl https://YOUR_USERNAME.github.io/flynn-tuf-repo/targets.json | jq .
```

If you see JSON content, it's working!

---

## Phase 3: Configure Flynn

### Step 11: Update Flynn Configuration

Edit `/home/philipp/GIT/flynn/builder/manifest.json`:

Find the `tuf` section:

```json
"tuf": {
  "repository": "https://YOUR_USERNAME.github.io/flynn-tuf-repo",
  "root_keys": [
    {"keytype":"ed25519","keyval":{"public":"6cfda23aa48f530aebd5b9c01030d06d02f25876b5508d681675270027af4731"}}
  ]
}
```

Replace with your actual repository and public key.

### Step 12: Update Tup Config

Edit `/home/philipp/GIT/flynn/tup.config`:

```
CONFIG_IMAGE_REPOSITORY=https://YOUR_USERNAME.github.io/flynn-tuf-repo
CONFIG_TUF_ROOT_KEYS=[{"keytype":"ed25519","keyval":{"public":"6cfda23aa48f530aebd5b9c01030d06d02f25876b5508d681675270027af4731"}}]
```

---

## Phase 4: Populate TUF with Flynn Components

**This is the most challenging part** because TUF is used during the build process.

### Option A: Build Flynn First (Recommended)

Since you're setting up a new TUF repository, you'll need to:

1. **Download a fallback flynn-host binary** from an archive, OR
2. **Build flynn-host directly** without TUF dependencies

Let's go with Option A - using an archived binary:

```bash
# Download flynn-host from archive (if available)
# This is a placeholder - you'll need to find an actual archived binary
wget https://example.com/archived-flynn-host.gz -O /tmp/flynn-host.gz

# Extract it
gunzip /tmp/flynn-host.gz
chmod +x /tmp/flynn-host

# Use it for initial bootstrap
sudo cp /tmp/flynn-host /home/philipp/GIT/flynn/build/bin/flynn-host

# Now build Flynn normally
cd /home/philipp/GIT/flynn
make
```

### Option B: Skip TUF for Initial Build

Modify `/home/philipp/GIT/flynn/script/build-flynn` to use local binaries only:

```bash
# Comment out the TUF download section (lines 77-107 approximately)
# Replace with direct binary copy or local build
```

This is a workaround for development purposes only.

---

## Phase 5: Populate TUF Repository with Built Components

Once you have a working Flynn build:

```bash
# Set environment variables
export TUF_TARGETS_PASSPHRASE="your-targets-passphrase"
export TUF_SNAPSHOT_PASSPHRASE="your-snapshot-passphrase"
export TUF_TIMESTAMP_PASSPHRASE="your-timestamp-passphrase"

# Export components to TUF repository
/home/philipp/GIT/flynn/script/export-components ~/flynn-tuf-setup/flynn-tuf-repo

# The export script will:
# - Hash binary files
# - Update targets.json
# - Create snapshot.json
# - Create timestamp.json
```

### Commit and Push Updates

```bash
cd ~/flynn-tuf-setup/flynn-tuf-repo

# The export script creates files in staged/ directory
# You need to commit them

# Sign the new targets
tuf sign targets
# Enter targets keys passphrase

# Create snapshot
tuf snapshot
# Enter snapshot keys passphrase

# Create timestamp
tuf timestamp
# Enter timestamp keys passphrase

# Commit changes
tuf commit

# Push to GitHub
git add .
git commit -m "Add Flynn components v0.1"
git push
```

---

## Testing Your Setup

### Step 13: Test TUF Repository

```bash
# Verify all files are accessible
curl https://YOUR_USERNAME.github.io/flynn-tuf-repo/root.json
curl https://YOUR_USERNAME.github.io/flynn-tuf-repo/targets.json
curl https://YOUR_USERNAME.github.io/flynn-tuf-repo/snapshot.json
curl https://YOUR_USERNAME.github.io/flynn-tuf-repo/timestamp.json

# Verify a specific target
curl https://YOUR_USERNAME.github.io/flynn-tuf-repo/targets/flynn-host.gz | head -c 100
```

### Step 14: Test Flynn Build

```bash
# Clean previous build
cd /home/philipp/GIT/flynn
make clean

# Build Flynn (this will use your TUF repository)
make
```

---

## Summary of What You Created

### Files Created

| Location | Purpose |
|----------|---------|
| `~/flynn-tuf-setup/flynn-tuf-repo/` | Local TUF repository |
| `https://github.com/YOUR_USERNAME/flynn-tuf-repo` | Public GitHub repo |
| `https://YOUR_USERNAME.github.io/flynn-tuf-repo/` | Public TUF endpoint |

### Configuration Files Modified

| File | Change |
|------|--------|
| `/home/philipp/GIT/flynn/builder/manifest.json` | Updated TUF repository URL and root keys |
| `/home/philipp/GIT/flynn/tup.config` | Updated TUF repository URL and root keys |

---

## Security Notes

1. **Keep your TUF passphrases secure** - they're in `keys/*.json`
2. **Use HTTPS** - GitHub Pages provides this automatically ✓
3. **Key rotation** - Consider rotating keys periodically
4. **Repository backup** - Keep backup of keys and TUF repository

---

## Troubleshooting

### Can't Access Repository

```bash
# Check GitHub Pages status
curl -I https://YOUR_USERNAME.github.io/flynn-tuf-repo/

# Should return HTTP/2 200
```

### TUF Verification fails

```bash
# Check public key matches
tuf root-keys  # Local
# Compare with root.json content
```

### Build fails to download

```bash
# Check DNS and connectivity
nslookup YOUR_USERNAME.github.io
ping YOUR_USERNAME.github.io
```

---

## Next Steps

1. Build additional Flynn components
2. Add more target files to TUF repository
3. Set up automated updates
4. Consider Cloudflare Pages for better performance

---

## Resources

- **Detailed guide**: `/home/philipp/projects/flynn/TUF_SETUP.md`
- **Quick start**: `/home/philipp/projects/flynn/TUF_QUICKSTART.md`
- **Developer plan**: `/home/philipp/projects/flynn/DEVELOPER_PLAN.md`
- **TUF docs**: http://theupdateframework.com/

---

**Total time**: 30-60 minutes  
**Cost**: Free (GitHub Pages)  
**Result**: Public TUF repository for Flynn
