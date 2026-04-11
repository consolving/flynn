# Flynn TUF Repository Setup Guide

This document provides detailed instructions for setting up a public TUF repository for Flynn.

**Important**: The original TUF repository (`https://dl.flynn.io/tuf`) has been shut down. You must create your own TUF repository to build Flynn.

---

## Table of Contents

1. [Overview](#overview)
2. [Prerequisites](#prerequisites)
3. [Setting Up a Public TUF Repository](#setting-up-a-public-tuf-repository)
4. [Alternative: Building Without TUF Repository](#alternative-building-without-tuf-repository)
5. [TUF Repository Commands Reference](#tuf-repository-commands-reference)
6. [Troubleshooting](#troubleshooting)

---

## Overview

### What is TUF?

The Update Framework (TUF) is a secure update framework that protects software update systems against:
- Rollback attacks
- Fork attacks
- Deprecation attacks
- Mix-and-match attacks
- Overriding target files

Flynn uses TUF to securely distribute binaries, Docker images, and manifests.

### Current State

- **Original TUF repo**: `https://dl.flynn.io/tuf` - **SHUT DOWN**
- **Original root key**: `6cfda23aa48f530aebd5b9c01030d06d02f25876b5508d681675270027af4731`

To build Flynn, you need to either:
1. Set up your own TUF repository (recommended for production use)
2. Use a modified build process that doesn't require the TUF repository

---

## Prerequisites

### Required Tools

```bash
# Install basic tools
sudo apt-get update
sudo apt-get install -y curl jq sha512sum

# Install Go (version 1.13+)
# See: https://golang.org/doc/install
go version  # should show go version go1.13+
```

### Optional: Docker

For building and testing, you'll need Docker 1.9.1:
```bash
# See /home/philipp/GIT/flynn/util/docker/install.sh
# Or install manually:
curl -fsSL https://get.docker.com/builds/Linux/x86_64/docker-1.9.1 -o /tmp/docker
sudo mv /tmp/docker /usr/local/bin/docker
sudo chmod +x /usr/local/bin/docker
```

---

## Setting Up a Public TUF Repository

### Option 1: Using a Static Web Server (Simple)

This option requires no cloud infrastructure and is good for testing.

#### Step 1: Install the TUF CLI

```bash
# Go to a temporary directory
mkdir -p ~/tuf-setup
cd ~/tuf-setup

# Get the TUF CLI from the vendor directory
export GOPATH=$(pwd)
go get github.com/flynn/go-tuf/cmd/tuf
go get github.com/flynn/go-tuf/cmd/tuf-client
```

#### Step 2: Initialize TUF Repository

```bash
# Create repository directory structure
mkdir -p ~/flynn-tuf-repo
cd ~/flynn-tuf-repo

# Initialize the repository
tuf init

# Generate signing keys (you'll be prompted for passphrases)
tuf gen-key root
tuf gen-key targets
tuf gen-key snapshot
tuf gen-key timestamp
```

**Warning**: Store the passphrases securely! You'll need them for future updates.

#### Step 3: Sign the Root Manifest

Copy the staged root manifest from the repo to a secure location for signing:

```bash
# The root key was generated on this machine, so it's already signed
# Copy the signed root to the repository
tuf sign root

# Commit the changes
tuf commit
```

#### Step 4: Export Flynn Components

This is where you add Flynn binaries and manifests to the TUF repository.

**Important**: Since the original TUF repository is shut down, you cannot use the exact same binaries. You have two options:

**Option A**: Build Flynn first, then export (detailed below)
**Option B**: Download archived snapshots if available

```bash
# First, you need to build Flynn
cd /home/philipp/GIT/flynn

# Build Flynn with your custom TUF repository
export TUF_TARGETS_PASSPHRASE="your-passphrase"
export TUF_SNAPSHOT_PASSPHRASE="your-passphrase"
export TUF_TIMESTAMP_PASSPHRASE="your-passphrase"

# Build the initial flynn-host binary (using a known good binary from cache or archived source)
# This is a chicken-and-egg problem - you need the TUF repo to build, but need to build to populate TUF

# For now, let's assume you have a working build
make
```

**Alternative approach**: You can skip the TUF dependency initially by modifying the build process to use local binaries only.

#### Step 5: Make Repository Publicly Accessible

**Simple HTTP server (for testing):**

```bash
# Install a simple HTTP server
sudo apt-get install -y python3

# Serve the TUF repository
cd ~/flynn-tuf-repo
python3 -m http.server 8080
```

**Production web server (Apache/Nginx):**

```bash
# Copy repository to web server directory
sudo cp -r ~/flynn-tuf-repo/repository /var/www/html/flynn-tuf

# Configure web server to serve static files
# Apache: /etc/apache2/sites-available/000-default.conf
# Nginx: /etc/nginx/sites-available/default
```

#### Step 6: Configure Flynn to Use Your TUF Repository

Modify `/home/philipp/GIT/flynn/builder/manifest.json`:

```json
{
  "tuf": {
    "repository": "http://your-server.com:8080",
    "root_keys": [
      {"keytype":"ed25519","keyval":{"public":"YOUR_PUBLIC_KEY_HEX"}}
    ]
  },
  ...
}
```

Get the public key:

```bash
tuf root-keys
# Output: [{"keytype":"ed25519","keyval":{"public":"hex_key_here"}}]
```

Also update `/home/philipp/GIT/flynn/tup.config`:

```
CONFIG_IMAGE_REPOSITORY=http://your-server.com:8080
CONFIG_TUF_ROOT_KEYS=[{"keytype":"ed25519","keyval":{"public":"YOUR_PUBLIC_KEY_HEX"}}]
```

#### Step 7: Building Flynn with Custom TUF

Due to the complexity of the initial bootstrap (building requires TUF, but TUF is populated by the build), you'll likely need to modify the build process.

**Simplified approach for initial build:**

1. Download a pre-built flynn-host from an archive or build it standalone
2. Use it to bootstrap the cluster
3. Build other components
4. Export to TUF repository

---

### Option 2: Using Amazon S3 (Recommended for Production)

This option provides better performance and reliability.

#### Step 1: Setup AWS S3 Bucket

```bash
# Install AWS CLI
pip install awscli

# Configure AWS credentials
aws configure
```

Create and configure S3 bucket:

```bash
# Create bucket (choose a unique name)
aws s3 mb s3://my-flynn-tuf-repo

# Enable static website hosting
aws s3 website s3://my-flynn-tuf-repo --index-document index.html

# Make bucket public
aws s3api put-bucket-policy --bucket my-flynn-tuf-repo --policy '{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": "*",
    "Action": ["s3:GetObject"],
    "Resource": "arn:aws:s3:::my-flynn-tuf-repo/*"
  }]
}'
```

#### Step 2: Copy TUF Repository to S3

```bash
# After creating the TUF repo locally (see Option 1)
cd ~/flynn-tuf-repo/repository

# Upload to S3
aws s3 sync . s3://my-flynn-tuf-repo/tuf

# Verify
aws s3 ls s3://my-flynn-tuf-repo/tuf
```

#### Step 3: Update Flynn Configuration

```bash
# Update builder/manifest.json
"repository": "https://my-flynn-tuf-repo.s3.amazonaws.com/tuf",

# Update tup.config
CONFIG_IMAGE_REPOSITORY=https://my-flynn-tuf-repo.s3.amazonaws.com/tuf
```

#### Step 4: Set Up CloudFront (Optional but Recommended)

```bash
# Create CloudFront distribution
aws cloudfront create-distribution \
  --origin-domain-name my-flynn-tuf-repo.s3.amazonaws.com \
  --default-root-object tuf

# Update DNS if using custom domain
```

---

## Alternative: Building Without TUF Repository

If setting up TUF is too complex for your use case, you can modify the build process to work without it.

### Modify `script/build-flynn`

The main build script downloads `flynn-host` from the TUF repository first. You can:

**Option 1**: Use a cached/backup copy of flynn-host

```bash
# If you have archived the original flynn-host binary
cp /path/to/archived/flynn-host /home/philipp/GIT/flynn/build/bin/

# Then run the build
make
```

**Option 2**: Build flynn-host standalone

```bash
# Extract the flynn-host build from Docker image manually
# This requires building Docker images first
cd /home/philipp/GIT/flynn

# Build the builder image
builder/img/go.sh

# Build flynn-host binary directly
make clean
make
```

**Option 3**: Disable TUF validation (NOT recommended for production)

This requires code changes to the TUF client in `vendor/github.com/flynn/go-tuf/client/client.go`.

---

## TUF Repository Commands Reference

### Basic Commands

```bash
# Initialize a new repository
tuf init [--consistent-snapshot=false]

# Generate a signing key for a role
tuf gen-key [--expires=<days>] <role>
# Roles: root, targets, snapshot, timestamp

# Add target files to the repository
tuf add [<path>...]
tuf add /path/to/binaries/*

# Remove target files
tuf remove [<path>...]

# Update snapshot manifest
tuf snapshot [--compression=<format>]

# Update timestamp manifest
tuf timestamp

# Sign a manifest
tuf sign <role>

# Commit staged changes to the repository
tuf commit

# Regenerate manifests from existing targets
tuf regenerate

# Clean staged files
tuf clean

# Get root keys
tuf root-keys
```

### Complete Repository Workflow

```bash
# 1. Initialize
tuf init
tuf gen-key root
tuf gen-key targets
tuf gen-key snapshot
tuf gen-key timestamp

# 2. Sign root (if root key generated on different machine)
tuf sign root

# 3. Add target files
tuf add bin/*
tuf add images/*

# 4. Update metadata
tuf snapshot
tuf timestamp

# 5. Commit
tuf commit

# 6. Publish to server
# Copy repository directory to web server or S3
```

---

## Troubleshooting

### Common Issues

#### 1. Cannot Download from TUF Repository

**Error**: `curl: (7) Failed to connect to dl.flynn.io`

**Solution**: 
- Update `builder/manifest.json` to use your repository URL
- Ensure the repository is accessible from the build machine
- Check firewall rules

#### 2. TUF Verification Failed

**Error**: `tuf: failed to verify signature`

**Solution**:
- Check that all root keys match
- Verify the public key in `builder/manifest.json` matches `tuf root-keys` output
- Ensure all manifests are properly signed

#### 3. Missing Signatures

**Error**: `not enough signatures`

**Solution**:
- Ensure all required role keys are generated and present
- Sign manifests with correct number of keys (check `root.json` thresholds)

#### 4. Build Hangs on Download

**Solution**:
- Check network connectivity
- Verify TUF repository URL is correct
- Try downloading manually to test:

```bash
curl -v http://your-server.com:8080/tuf/root.json
```

### Debugging Tips

```bash
# Enable verbose output in build scripts
./script/build-flynn -v

# Check TUF repository structure
tree ~/flynn-tuf-repo

# Verify manifest signatures
jq . ~/flynn-tuf-repo/repository/root.json
jq . ~/flynn-tuf-repo/repository/targets.json

# Test TUF client
tuf-client -h
```

### Recovery

If your TUF repository gets corrupted:

```bash
# Clean and reinitialize
cd ~/flynn-tuf-repo
rm -rf repository staged
tuf init

# Regenerate keys (WARNING: This changes all hashes!)
tuf gen-key root
tuf gen-key targets
tuf gen-key snapshot
tuf gen-key timestamp

# Re-add all target files
tuf add /path/to/all/binaries/*
tuf snapshot
tuf timestamp
tuf commit
```

---

## Security Best Practices

### 1. Key Management

- **Root keys**: Store off-line in secure location
- **Targets keys**: Store on build server with restricted access
- **Snapshot/Timestamp keys**: Can be on TUF server

### 2. Repository Security

- Use HTTPS for all TUF repository access
- Implement access controls for write operations
- Regularly rotate keys
- Keep repository backup

### 3. Build Security

- Verify all source code before building
- Use reproducible builds where possible
-签所有 binaries before adding to TUF
- Audit TUF repository changes

---

## References

- **TUF Specification**: http://theupdateframework.com/
- **go-tuf Documentation**: https://github.com/flynn/go-tuf
- **Original Flynn TUF**: https://dl.flynn.io/tuf (SHUT DOWN)
- **Flynn Docs**: /home/philipp/GIT/flynn/docs/

---

**Document Version**: 1.0  
**Last Updated**: April 11, 2026
