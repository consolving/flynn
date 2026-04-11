# Quick Start: Public TUF Repository

This is a simplified guide to get a basic TUF repository running publicly in under 30 minutes.

---

## TL;DR - Fastest Option

Use a free tier GitHub Pages or Cloudflare Pages for a simple, free, public TUF repository.

---

## Option 1: GitHub Pages (Recommended for Testing)

### Time: ~10 minutes  
### Cost: Free  
### Public: Yes  
### Ease: ★★★★★

#### Steps:

1. **Create GitHub Repository**

```bash
# Create a new GitHub repo: flynn-tuf-repo
git init ~/flynn-tuf-repo
cd ~/flynn-tuf-repo
git remote add origin https://github.com/YOUR_USERNAME/flynn-tuf-repo.git
```

2. **Setup TUF in the Repo**

```bash
# Install TUF CLI
go install github.com/flynn/go-tuf/cmd/tuf@latest

# Initialize repository
tuf init

# Generate keys (you'll need passphrases - remember them!)
tuf gen-key root
tuf gen-key targets  
tuf gen-key snapshot
tuf gen-key timestamp

# Sign root manifest
tuf sign root

# Commit to git
git add .
git commit -m "Initial TUF repository setup"
git push origin master
```

3. **Enable GitHub Pages**

- Go to repo Settings → Pages
- Source: master branch
- Save

Your TUF repository is now at: `https://YOUR_USERNAME.github.io/flynn-tuf-repo/`

4. **Configure Flynn**

Update `/home/philipp/GIT/flynn/builder/manifest.json`:

```json
{
  "tuf": {
    "repository": "https://YOUR_USERNAME.github.io/flynn-tuf-repo",
    "root_keys": [
      {"keytype":"ed25519","keyval":{"public":"PASTE_YOUR_PUBLIC_KEY_HERE"}}
    ]
  }
}
```

Get the public key:
```bash
tuf root-keys
```

**Note**: GitHub Pages only serves static files. You'll need to manually add target files to the `repository/targets/` directory.

---

## Option 2: Cloudflare Pages (Free Tier)

### Time: ~15 minutes  
### Cost: Free  
### Public: Yes  
### Ease: ★★★★☆

1. **Create TUF Repository**

```bash
mkdir ~/flynn-tuf && cd ~/flynn-tuf
tuf init
tuf gen-key root
tuf gen-key targets
tuf gen-key snapshot
tuf gen-key timestamp
tuf sign root
```

2. **Upload to Cloudflare**

You'll need to upload the `repository` directory manually via Cloudflare Dashboard or use the API.

3. **Configure Custom Domain**

Cloudflare Pages will give you a URL like: `https://flynn-tuf.pages.dev/`

4. **Update Flynn Config**

```json
{
  "tuf": {
    "repository": "https://flynn-tuf.pages.dev",
    "root_keys": [...]
  }
}
```

---

## Option 3: AWS S3 (Production-Ready)

### Time: ~20 minutes  
### Cost: ~$0.50/month (very low traffic)  
### Public: Yes  
### Ease: ★★★☆☆

1. **Create S3 Bucket**

```bash
aws s3 mb s3://flynn-tuf-yourname
aws s3 website s3://flynn-tuf-yourname --index-document index.html
```

2. **Setup TUF Locally**

```bash
cd ~/flynn-tuf
tuf init
tuf gen-key root
tuf gen-key targets
tuf gen-key snapshot
tuf gen-key timestamp
tuf sign root
```

3. **Upload to S3**

```bash
aws s3 sync ~/flynn-tuf/repository s3://flynn-tuf-yourname
```

4. **Make Public**

```bash
aws s3api put-bucket-policy --bucket flynn-tuf-yourname --policy '{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": "*",
    "Action": ["s3:GetObject"],
    "Resource": "arn:aws:s3:::flynn-tuf-yourname/*"
  }]
}'
```

5. **Verify**

```
https://flynn-tuf-yourname.s3.amazonaws.com/tuf/root.json
```

6. **Configure Flynn**

```json
{
  "tuf": {
    "repository": "https://flynn-tuf-yourname.s3.amazonaws.com",
    "root_keys": [...]
  }
}
```

---

## Option 4: Free Web Hosting (Vercel/Netlify)

### Time: ~10 minutes  
### Cost: Free  
### Public: Yes  
### Ease: ★★★★☆

### Vercel:

1. Create repository with TUF files
2. Connect to Vercel
3. Deploy
4. Use `https://your-project.vercel.app`

### Netlify:

1. Drag and drop `repository` folder to Netlify
2. Or connect Git repo
3. Use `https://your-site.netlify.app`

---

## Important Notes About TUF Repository Content

### You Need to Populate Targets

The TUF repository structure requires target files in `repository/targets/`:

```
repository/
├── root.json
├── targets.json
├── snapshot.json
└── timestamp.json
targets/
├── <binary-name>
├── <image-manifest.json>
└── ...
```

### Minimum Required Files for Flynn

For Flynn to work, you need at minimum:

1. **flynn-host.gz** - The Flynn host binary
2. **Builder image manifests** - JSON files describing Docker images
3. **Bootstrap manifest** - `bootstrap-manifest.json`

### Simplified Approach

Since fully populating TUF requires building Flynn first (chicken-egg problem), consider:

1. **Use cached binaries**: If you have access to archived Flynn binaries from before shutdown
2. **Build only what you need**: Start with minimal components
3. **Disable TUF validation**: Modify source code (not recommended for production)

---

## Verifying Your Setup

Once you have your TUF repository set up:

```bash
# Test access
curl https://your-tuf-repo.com/root.json

# Verify structure
curl https://your-tuf-repo.com/targets.json
curl https://your-tuf-repo.com/snapshot.json
curl https://your-tuf-repo.com/timestamp.json

# Test with TUF client
tuf-client init --repository https://your-tuf-repo.com
```

---

## Security Checklist

- [ ] Use HTTPS (not HTTP)
- [ ] Root keys stored securely (off-line recommended)
- [ ] Public keys match in Flynn config
- [ ] Repository accessible from build machines
- [ ] Basic authentication for writes (if applicable)

---

## Next Steps

After setting up the TUF repository:

1. Build Flynn (see `/home/philipp/projects/flynn/DEVELOPER_PLAN.md`)
2. Populate the `repository/targets/` directory
3. Sign and commit changes
4. Update repository on server
5. Test installation

---

## Need Help?

Check the comprehensive guide: `/home/philipp/projects/flynn/TUF_SETUP.md`

---

**Quick Summary**: For fastest results, use **GitHub Pages** (Option 1) - it's free, public, and requires minimal setup.
