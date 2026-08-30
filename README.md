# SaveIt Web 🌐

> A Go + Vercel serverless web app that downloads videos and photos from social media.
> Pairs with the **SaveIt Android app** for cross-platform media saving.

**Live demo:** `https://your-project.vercel.app`

---

## Features

| Feature | Detail |
|---------|--------|
| 12+ platforms | YouTube, Instagram, TikTok, Twitter/X, Facebook, Reddit, Pinterest, Twitch, Vimeo, SoundCloud, Dailymotion, Bilibili |
| Quality picker | Max / 4K / 1440p / 1080p / 720p / 480p / 360p |
| Audio-only mode | Extracts MP3 from any video |
| Instagram carousel | Shows a grid of all photos/videos in a post |
| Download proxy | Adds `Content-Disposition` so files save correctly |
| Download history | Stored in `localStorage` — no server, no signup |
| Dark UI | Glassmorphism, animated, fully responsive |
| Zero JS deps | Vanilla JS + pure Go stdlib — no bloat |

---

## Stack

```
public/index.html   ← Single-page frontend (HTML + CSS + JS, zero deps)
api/resolve.go      ← POST /api/resolve  — resolves URLs via cobalt.tools
api/proxy.go        ← GET  /api/proxy    — streams files with download header
api/health.go       ← GET  /api/health   — deployment health check
go.mod              ← Pure stdlib, no external packages
vercel.json         ← Routing + build config
```

---

## Deploy to Vercel in 5 minutes

### Option A — Vercel Dashboard (easiest)

1. Push this repo to GitHub
2. Go to [vercel.com/new](https://vercel.com/new)
3. Import your repository → **Deploy**
4. Done. Vercel detects Go + static files automatically.

### Option B — GitHub Actions (auto-deploy on push)

**Step 1 — Get Vercel credentials**
```bash
npm i -g vercel
vercel login
vercel link          # creates .vercel/project.json with org + project IDs
```

**Step 2 — Add GitHub Secrets**

Go to your repo → **Settings → Secrets and variables → Actions → New secret**

| Secret name | Where to find it |
|-------------|-----------------|
| `VERCEL_TOKEN` | vercel.com → Account Settings → Tokens → Create |
| `VERCEL_ORG_ID` | `.vercel/project.json` → `"orgId"` |
| `VERCEL_PROJECT_ID` | `.vercel/project.json` → `"projectId"` |

**Step 3 — Push**
```bash
git add .
git commit -m "Initial deploy"
git push origin main
```

GitHub Actions runs automatically → check the **Actions** tab → your site is live.

---

## Environment variables

Set these in the Vercel dashboard under **Settings → Environment Variables**:

| Variable | Default | Description |
|----------|---------|-------------|
| `COBALT_API` | `https://api.cobalt.tools` | cobalt.tools instance URL. Replace with your own self-hosted instance for production. |

### Self-host cobalt (recommended for production)

```bash
# Docker (easiest)
docker run -d \
  -p 9000:9000 \
  -e API_URL=http://localhost:9000 \
  ghcr.io/imputnet/cobalt:latest

# Then set COBALT_API=http://your-server:9000 in Vercel
```

See [cobalt.tools GitHub](https://github.com/imputnet/cobalt) for full self-hosting docs.

---

## API Reference

### `POST /api/resolve`

Resolves a social-media URL to a direct download link.

**Request:**
```json
{
  "url":       "https://www.youtube.com/watch?v=...",
  "quality":   "1080",
  "audioOnly": false,
  "muteVideo": false
}
```

**Response — direct download:**
```json
{
  "status":   "direct",
  "platform": "YouTube",
  "url":      "https://cdn.example.com/video.mp4",
  "filename": "YouTube_video_1080p.mp4"
}
```

**Response — picker (e.g. Instagram carousel):**
```json
{
  "status":   "picker",
  "platform": "Instagram",
  "picker": [
    { "type": "photo", "url": "https://...", "thumb": "https://..." },
    { "type": "video", "url": "https://...", "thumb": "https://..." }
  ]
}
```

### `GET /api/proxy?url=...&filename=...`

Streams a remote file through the server with `Content-Disposition: attachment` so browsers download it instead of opening it.

⚠ **Vercel limits:**
- Hobby plan: 4.5 MB response, 10 s timeout
- Pro plan: 50 MB response, 300 s timeout
- For large videos: use the **Direct Link** button in the UI instead

### `GET /api/health`

Returns server status + configured cobalt instance.

---

## Local development

```bash
# Install Vercel CLI
npm i -g vercel

# Run locally (uses vercel dev — no need for Android Studio or anything)
vercel dev

# App is live at http://localhost:3000
```

---

## Project structure

```
saveit-web/
├── .github/
│   └── workflows/
│       └── deploy.yml       ← Auto-deploy on push to main
├── api/
│   ├── resolve.go           ← URL resolver (calls cobalt.tools)
│   ├── proxy.go             ← Download proxy with SSRF protection
│   └── health.go            ← Health check
├── public/
│   └── index.html           ← Complete SPA (HTML + CSS + JS)
├── go.mod                   ← Go module (pure stdlib, no deps)
├── vercel.json              ← Vercel routing + build config
├── .gitignore
└── README.md
```

---

## Responsible use

- For **personal use only**
- Do not redistribute downloaded content without permission
- Respect each platform's Terms of Service
- Do not use for commercial purposes without proper licensing

---

## License

MIT — see [cobalt.tools](https://github.com/imputnet/cobalt) for the extraction engine license.
