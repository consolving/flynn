# TUF Repository Setup Notes

## Overview

This repository documents the setup of a TUF (The Update Framework) repository for Flynn builds.

## Current State

- **Original TUF repo**: https://dl.flynn.io/tuf - **SHUT DOWN**
- **Original root key**: `6cfda23aa48f530aebd5b9c01030d06d02f25876b5508d681675270027af4731`

## Documentation Reference

See `/home/philipp/projects/flynn/` for comprehensive guides:
- `DEVELOPER_PLAN.md` - Build process overview
- `TUF_SETUP.md` - Detailed TUF repository setup
- `TUF_QUICKSTART.md` - Quick setup guide
- `STEP_BY_STEP_TUF.md` - Complete walkthrough

## Quick Setup Options

### Option 1: GitHub Pages (Recommended for Testing)
**Time**: ~10 minutes | **Cost**: Free | **Public**: Yes

1. Create GitHub repo `flynn-tuf-repo`
2. Initialize TUF locally and push to GitHub
3. Enable GitHub Pages in repo settings
4. Configure Flynn to use `https://YOUR_USERNAME.github.io/flynn-tuf-repo`

### Option 2: AWS S3 (Production)
**Time**: ~20 minutes | **Cost**: ~$0.50/month | **Public**: Yes

1. Create S3 bucket
2. Enable static website hosting
3. Upload TUF repository files
4. Configure Flynn to use bucket URL

## Key Commands

```bash
# Install TUF CLI
go install github.com/flynn/go-tuf/cmd/tuf@latest

# Initialize repository
tuf init

# Generate keys
tuf gen-key root
tuf gen-key targets
tuf gen-key snapshot
tuf gen-key timestamp

# Sign and commit
tuf sign root
tuf commit

# Get root keys for config
tuf root-keys
```

## Configuration Files to Update

1. `/home/philipp/GIT/flynn/builder/manifest.json` - Update TUF repo URL and root keys
2. `/home/philipp/GIT/flynn/tup.config` - Update TUF repo URL and root keys

## Critical Note

The TUF repository is used during the build process. Since the original is shut down, you must:
1. Set up your own TUF repository first, OR
2. Modify the build to skip TUF for initial bootstrap

## Security

- Keep TUF passphrases secure (stored in `keys/*.json`)
- Use HTTPS for production
- Store root keys offline
- Regular key rotation recommended

## Status

- [x] TUF repository created
- [x] Public access configured
- [ ] Flynn build tested
- [ ] Documentation complete
