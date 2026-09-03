// server.go — local development server for SaveIt Web
//
// Usage:
//   go run server.go
//   go run server.go -port 8080
//   COBALT_API=http://localhost:9000 go run server.go
//
// This file is ignored by Vercel (it only processes api/*.go).
// It replicates all three Vercel functions in a single binary so you
// can test everything locally with no Node.js or Vercel CLI required.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// ── Config ────────────────────────────────────────────────────────────────────

var (
	port      = flag.String("port", envOr("PORT", "8080"), "listen port")
	cobaltAPI = flag.String("cobalt", envOr("COBALT_API", "https://api.cobalt.tools"), "cobalt.tools base URL")
	staticDir = flag.String("static", "public", "directory of static files to serve")
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ── Entry point ───────────────────────────────────────────────────────────────

func main() {
	flag.Parse()

	mux := http.NewServeMux()

	// API routes — mirror the Vercel function paths exactly
	mux.HandleFunc("/api/resolve", resolveHandler)
	mux.HandleFunc("/api/proxy", proxyHandler)
	mux.HandleFunc("/api/health", healthHandler)

	// Serve the public/ frontend for everything else
	fs := http.FileServer(http.Dir(*staticDir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// SPA fallback: unknown paths → index.html
		path := *staticDir + r.URL.Path
		if _, err := os.Stat(path); os.IsNotExist(err) {
			http.ServeFile(w, r, *staticDir+"/index.html")
			return
		}
		fs.ServeHTTP(w, r)
	})

	addr := ":" + *port
	log.Printf("┌─────────────────────────────────────────────────")
	log.Printf("│  SaveIt Web  —  local dev server")
	log.Printf("│  Frontend : http://localhost%s", addr)
	log.Printf("│  Resolve  : http://localhost%s/api/resolve", addr)
	log.Printf("│  Proxy    : http://localhost%s/api/proxy", addr)
	log.Printf("│  Health   : http://localhost%s/api/health", addr)
	log.Printf("│  Cobalt   : %s", *cobaltAPI)
	log.Printf("└─────────────────────────────────────────────────")

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// ── /api/resolve ──────────────────────────────────────────────────────────────

type resolveReq struct {
	URL       string `json:"url"`
	Quality   string `json:"quality"`
	AudioOnly bool   `json:"audioOnly"`
	MuteVideo bool   `json:"muteVideo"`
}

type resolveResp struct {
	Status   string       `json:"status"`
	Platform string       `json:"platform"`
	URL      string       `json:"url,omitempty"`
	Filename string       `json:"filename,omitempty"`
	Picker   []pickerItem `json:"picker,omitempty"`
	Audio    string       `json:"audio,omitempty"`
	Error    string       `json:"error,omitempty"`
}

type pickerItem struct {
	Type  string `json:"type"`
	URL   string `json:"url"`
	Thumb string `json:"thumb,omitempty"`
}

type cobaltReq struct {
	URL           string `json:"url"`
	VideoQuality  string `json:"videoQuality"`
	DownloadMode  string `json:"downloadMode"`
	AudioFormat   string `json:"audioFormat"`
	FilenameStyle string `json:"filenameStyle"`
}

type cobaltResp struct {
	Status string `json:"status"`
	URL    string `json:"url"`
	Filename string `json:"filename"`
	Picker   []struct {
		Type  string `json:"type"`
		URL   string `json:"url"`
		Thumb string `json:"thumb"`
	} `json:"picker"`
	Audio string `json:"audio"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func resolveHandler(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions { w.WriteHeader(http.StatusNoContent); return }
	if r.Method != http.MethodPost { jsonErr(w, 405, "method not allowed"); return }

	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil { jsonErr(w, 400, "cannot read body"); return }
	defer r.Body.Close()

	var req resolveReq
	if err := json.Unmarshal(body, &req); err != nil { jsonErr(w, 400, "invalid JSON"); return }

	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" { jsonErr(w, 400, "url is required"); return }

	parsed, err := url.ParseRequestURI(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		jsonErr(w, 400, "invalid url")
		return
	}

	quality := req.Quality
	if quality == "" { quality = "1080" }

	mode := "auto"
	if req.AudioOnly { mode = "audio" } else if req.MuteVideo { mode = "mute" }

	cr, err := doCobalt(*cobaltAPI, req.URL, quality, mode)
	if err != nil {
		log.Printf("[resolve] cobalt error: %v", err)
		jsonErr(w, 502, "resolution failed: "+err.Error())
		return
	}

	resp := resolveResp{Platform: detectPlatform(req.URL)}
	switch cr.Status {
	case "redirect", "stream", "tunnel":
		resp.Status = "direct"
		resp.URL = cr.URL
		resp.Filename = cr.Filename
	case "picker":
		resp.Status = "picker"
		resp.Audio = cr.Audio
		for _, it := range cr.Picker {
			resp.Picker = append(resp.Picker, pickerItem{Type: it.Type, URL: it.URL, Thumb: it.Thumb})
		}
	default:
		resp.Status = "error"
		resp.Error = "unexpected cobalt status: " + cr.Status
	}

	log.Printf("[resolve] %s → %s (%s)", req.URL, resp.Status, resp.Platform)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func doCobalt(base, mediaURL, quality, mode string) (*cobaltResp, error) {
	pl := cobaltReq{URL: mediaURL, VideoQuality: quality, DownloadMode: mode, AudioFormat: "mp3", FilenameStyle: "pretty"}
	b, _ := json.Marshal(pl)

	client := &http.Client{Timeout: 25 * time.Second}
	req, err := http.NewRequest("POST", base+"/", bytes.NewReader(b))
	if err != nil { return nil, err }
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "SaveIt-Local/1.0")

	resp, err := client.Do(req)
	if err != nil { return nil, fmt.Errorf("network: %w", err) }
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil { return nil, fmt.Errorf("read: %w", err) }

	var cr cobaltResp
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, fmt.Errorf("parse error — raw: %s", truncate(string(raw), 120))
	}
	if cr.Status == "error" || cr.Status == "rate-limit" {
		code := "unknown"
		if cr.Error != nil { code = cr.Error.Code }
		return nil, fmt.Errorf("cobalt: %s", friendlyErr(code))
	}
	return &cr, nil
}

func friendlyErr(code string) string {
	switch {
	case strings.Contains(code, "unsupported"): return "URL not supported"
	case strings.Contains(code, "private"):     return "content is private"
	case strings.Contains(code, "age"):         return "age-restricted content"
	case strings.Contains(code, "limit"):       return "rate limited — try again soon"
	default:                                    return code
	}
}

// ── /api/proxy ────────────────────────────────────────────────────────────────

// Local server has no 4.5 MB limit — we'll proxy up to 2 GB
const localMaxBytes = 2 * 1024 * 1024 * 1024

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	setCORSProxy(w)
	if r.Method == http.MethodOptions { w.WriteHeader(http.StatusNoContent); return }
	if r.Method != http.MethodGet { http.Error(w, "method not allowed", 405); return }

	rawURL  := strings.TrimSpace(r.URL.Query().Get("url"))
	filename := strings.TrimSpace(r.URL.Query().Get("filename"))
	if rawURL == "" { http.Error(w, "url param required", 400); return }
	if filename == "" { filename = "download" }

	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		http.Error(w, "invalid url", 400)
		return
	}
	if isPrivateHost(parsed.Hostname()) {
		http.Error(w, "private host blocked", 403)
		return
	}

	client := &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 { return http.ErrUseLastResponse }
			if isPrivateHost(req.URL.Hostname()) { return fmt.Errorf("redirect to private host") }
			return nil
		},
	}

	req, _ := http.NewRequest("GET", rawURL, nil)
	if rng := r.Header.Get("Range"); rng != "" { req.Header.Set("Range", rng) }
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SaveIt-Proxy/1.0)")
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil { http.Error(w, "upstream error: "+err.Error(), 502); return }
	defer resp.Body.Close()

	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(h); v != "" { w.Header().Set(h, v) }
	}
	if !strings.Contains(filename, ".") {
		filename += extFromContentType(resp.Header.Get("Content-Type"))
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeFilename(filename)))
	w.Header().Set("Access-Control-Allow-Origin", "*")

	log.Printf("[proxy] %s → %s", filename, truncate(rawURL, 60))
	w.WriteHeader(resp.StatusCode)
	n, _ := io.Copy(w, io.LimitReader(resp.Body, localMaxBytes))
	log.Printf("[proxy] streamed %.2f MB", float64(n)/1024/1024)
}

// ── /api/health ───────────────────────────────────────────────────────────────

func healthHandler(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":        true,
		"service":   "saveit-web (local)",
		"version":   "1.0.0",
		"time":      time.Now().UTC().Format(time.RFC3339),
		"cobaltApi": *cobaltAPI,
		"port":      *port,
	})
}

// ── Platform detection ────────────────────────────────────────────────────────

var platformPatterns = []struct {
	re   *regexp.Regexp
	name string
}{
	{regexp.MustCompile(`(?i)(youtube\.com|youtu\.be)`), "YouTube"},
	{regexp.MustCompile(`(?i)instagram\.com`),           "Instagram"},
	{regexp.MustCompile(`(?i)tiktok\.com`),              "TikTok"},
	{regexp.MustCompile(`(?i)(twitter\.com|x\.com)`),   "Twitter / X"},
	{regexp.MustCompile(`(?i)(facebook\.com|fb\.watch)`),"Facebook"},
	{regexp.MustCompile(`(?i)reddit\.com`),              "Reddit"},
	{regexp.MustCompile(`(?i)pinterest\.`),              "Pinterest"},
	{regexp.MustCompile(`(?i)twitch\.tv`),               "Twitch"},
	{regexp.MustCompile(`(?i)vimeo\.com`),               "Vimeo"},
	{regexp.MustCompile(`(?i)soundcloud\.com`),          "SoundCloud"},
	{regexp.MustCompile(`(?i)dailymotion\.com`),         "Dailymotion"},
	{regexp.MustCompile(`(?i)bilibili\.com`),            "Bilibili"},
}

func detectPlatform(rawURL string) string {
	for _, p := range platformPatterns {
		if p.re.MatchString(rawURL) { return p.name }
	}
	return "Unknown"
}

// ── SSRF protection ───────────────────────────────────────────────────────────

func isPrivateHost(host string) bool {
	// strip port
	h := host
	if i := strings.LastIndex(h, ":"); i > strings.LastIndex(h, "]") { h = h[:i] }
	h = strings.Trim(h, "[]")

	addrs, err := net.LookupHost(h)
	if err != nil { return true } // unresolvable → block

	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil { return true }
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsUnspecified() || ip.IsMulticast() { return true }
	}
	return false
}

// ── Shared helpers ────────────────────────────────────────────────────────────

func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func setCORSProxy(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Range")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Content-Disposition")
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func extFromContentType(ct string) string {
	ct = strings.ToLower(strings.Split(ct, ";")[0])
	switch ct {
	case "video/mp4":              return ".mp4"
	case "video/webm":             return ".webm"
	case "video/quicktime":        return ".mov"
	case "audio/mpeg", "audio/mp3":return ".mp3"
	case "audio/ogg":              return ".ogg"
	case "audio/wav":              return ".wav"
	case "image/jpeg":             return ".jpg"
	case "image/png":              return ".png"
	case "image/webp":             return ".webp"
	case "image/gif":              return ".gif"
	default:                       return ""
	}
}

func sanitizeFilename(name string) string {
	r := strings.NewReplacer(`"`, "", `\`, "", "\n", "", "\r", "", "/", "-")
	s := r.Replace(name)
	if len(s) > 200 { s = s[:200] }
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n { return s }
	return s[:n] + "…"
}
