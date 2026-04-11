# Creating a Public TUF Repository for Flynn

## Executive Summary

This guide shows how to create a publicly accessible TUF repository for Flynn, since the original repository (https://dl.flynn.io/tuf) has been shut down.

## Why TUF?

TUF (The Update Framework) provides:
- Secure software updates
- Protection against rollback attacks
- Role-based key management
- CDN compromise resistance

## Repository Hosting Options

### Option A: GitHub Pages (Easiest, Free)
- **Pros**: Free, automatic HTTPS, easy setup
- **Cons**: Limited to static files, manual updates
- **URL format**: `https://username.github.io/flynn-tuf-repo/`

### Option B: AWS S3 (Production-Ready)
- **Pros**: Scalable, reliable, cheap (~$0.50/month)
- **Cons**: Requires AWS account, more setup
- **URL format**: `https://bucket-name.s3.amazonaws.com/`

### Option C: Cloudflare Pages (Alternative)
- **Pros**: Free tier, fast CDN, easy deployment
- **Cons**: Less common than GitHub Pages
- **URL format**: `https://project.pages.dev/`

### Option D: Custom Web Server
- **Pros**: Full control, can be hosted anywhere
- **Cons**: Requires server management, security responsibility
- **URL format**: Your domain TUF subdirectory

## Step-by-Step: GitHub Pages Setup

### 1. Prerequisites

```bash
# Install Go (required for TUF CLI)
go version  # Should be 1.13+

# Install jq for JSON processing
sudo apt-get install -y jq
```

### 2. Create Local TUF Repository

```bash
mkdir -p ~/flynn-tuf-setup
cd ~/flynn-tuf-setup

# Install TUF CLI
go install github.com/flynn/go-tuf/cmd/tuf@latest
export PATH=$PATH:$(go env GOPATH)/bin

# Initialize repository
mkdir flynn-tuf && cd flynn-tuf
tuf init

# Generate signing keys (you'll be prompted for passphrases)
tuf gen-key root
tuf gen-key targets
tuf gen-key snapshot
tuf gen-key timestamp

# Sign root manifest
tuf sign root

# Commit changes
tuf commit

# Get root keys (you'll need this for Flynn config)
tuf root-keys
```

### 3. Push to GitHub

```bash
# Initialize git
git init

# Copy repository to root (GitHub Pages requirement)
cp -r repository/* .
cp -r keys/ .

# Add and commit
git add .
git commit -m "Initial Flynn TUF repository"

# Create GitHub repo and push
git remote add origin https://github.com/YOUR_USERNAME/flynn-tuf-repo.git
git branch -m main
git push -u origin main
```

### 4. Enable GitHub Pages

1. Go to your repo: `https://github.com/YOUR_USERNAME/flynn-tuf-repo`
2. Click **Settings** → **Pages**
3. Under "Build and deployment":
   - Source: `Deploy from a branch`
   - Branch: `main`
   - Folder: `/ (root)`
4. Click **Save**

### 5. Verify Repository

```bash
# Test the endpoints
curl https://YOUR_USERNAME.github.io/flynn-tuf-repo/root.json | jq .
curl https://YOUR_USERNAME.github.io/flynn-tuf-repo/targets.json | jq .
```

You should see JSON metadata files.

### 6. Configure Flynn

Update `/home/philipp/GIT/flynn/builder/manifest.json`:

```json
"tuf": {
  "repository": "https://YOUR_USERNAME.github.io/flynn-tuf-repo",
  "root_keys": [
    {"keytype":"ed25519","keyval":{"public":"YOUR_PUBLIC_KEY"}}
  ]
}
```

Update `/home/philipp/GIT/flynn/tup.config`:

```
CONFIG_IMAGE_REPOSITORY=https://YOUR_USERNAME.github.io/flynn-tuf-repo
CONFIG_TUF_ROOT_KEYS=[{"keytype":"ed25519","keyval":{"public":"YOUR_PUBLIC_KEY"}}]
```

### 7. Populate with Flynn Components

**Note**: This is the tricky part - you need to build Flynn to populate TUF, but TUF is used to build Flynn.

**Solution**: Use a fallback binary or modify build to temporarily skip TUF:

```bash
# Option 1: Download archived flynn-host (if available)
wget URL_TO_ARCHIVED_FLYNN_HOST -O /tmp/flynn-host.gz
gunzip /tmp/flynn-host.gz
sudo cp /tmp/flynn-host /home/philipp/GIT/flynn/build/bin/flynn-host

# Option 2: Modify build script to skip TUF for initial bootstrap

# Then build Flynn
cd /home/philipp/GIT/flynn
make
```

## Repository Structure

After setup, your TUF repository should look like:

```
repository/
├── root.json          # Root of trust
├── targets.json       # List of target files
├── snapshot.json      # Snapshot of targets
├── timestamp.json     # Timestamp metadata
└── targets/           # Hashed target files
    ├── flynn-host.gz
    ├── <image>.json
    └── ...
```

## Security Best Practices

1. **Keys**: Store `keys/` directory securely (offline recommended)
2. **Passphrases**: Use strong passwords, store in password manager
3. **HTTPS**: Always use HTTPS in production (GitHub Pages does this automatically)
4. **Monitoring**: Set up alerts for repository changes
5. **Backups**: Regular backups of keys and repository

## Testing Your Setup

```bash
# Test TUF client can access your repository
cd ~
tuf init
tuf config repository https://YOUR_USERNAME.github.io/flynn-tuf-repo
tuf config root_keys "$(tuf root-keys)"

# Verify metadata
curl https://YOUR_USERNAME.github.io/flynn-tuf-repo/root.json
curl https://YOUR_USERNAME.github.io/flynn-tuf-repo/targets.json
```

## Adding New Releases

When you have new Flynn components to add:

```bash
cd ~/flynn-tuf-setup/flynn-tuf

# Add new binaries
tuf add /path/to/new/binary.gz

# Update metadata
tuf sign targets
tuf snapshot
tuf timestamp

# Commit and push
git add .
git commit -m "Add new Flynn release"
git push
```

## Troubleshooting

### Repository Not Accessible
```bash
# Check GitHub Pages status
curl -I https://YOUR_USERNAME.github.io/flynn-tuf-repo/

# Should return HTTP/2 200
```

### TUF Verification Failed
```bash
# Check public key matches
tuf root-keys  # Local
jq .signed.embedded.timestamp.signed.keys ~/flynn-tuf/repository/root.json  # Remote
```

### Build Can't Download
```bash
# Test manual download
curl -v https://YOUR_USERNAME.github.io/flynn-tuf-repo/tuf/root.json
```

## Costs

- **GitHub Pages**: Free
- **AWS S3**: ~$0.50/month (low traffic)
- **Cloudflare Pages**: Free tier
- **Custom Server**: Your hosting costs

## Next Steps

1. Set up automatic updates for your TUF repository
2. Consider CloudflarePages for better performance
3. Document your release process
4. Set up key rotation schedule

## References

- **Comprehensive Guide**: `/home/philipp/projects/flynn/TUF_SETUP.md`
- **Quick Start**: `/home/philipp/projects/flynn/TUF_QUICKSTART.md`
- **Step By Step**: `/home/philipp/projects/flynn/STEP_BY_STEP_TUF.md`
- **TUF Spec**: http://theupdateframework.com/
- **go-tuf**: https://github.com/flynn/go-tuf
