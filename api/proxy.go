// api/proxy.go — GET /api/proxy?url=...&filename=...
// Streams a remote file through the server with a Content-Disposition header
// so the browser treats it as a download.
//
// ⚠ Vercel Hobby plan: 4.5 MB response limit & 10 s timeout.
//   Large video files will fail; the frontend falls back to opening the direct URL.
//   For production use, upgrade to Vercel Pro (50 MB, 300 s) or self-host.
package handler

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Maximum bytes we'll proxy (48 MB — stays under Vercel Pro limit)
const maxProxyBytes = 48 * 1024 * 1024

func ProxyHandler(w http.ResponseWriter, r *http.Request) {
	proxyCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	filename := strings.TrimSpace(r.URL.Query().Get("filename"))

	if rawURL == "" {
		http.Error(w, "url parameter is required", http.StatusBadRequest)
		return
	}
	if filename == "" {
		filename = "download"
	}

	// ── Validate target URL ───────────────────────────────────────────────────
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}
	if isBlockedHost(parsed.Hostname()) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}

	// ── Fetch ─────────────────────────────────────────────────────────────────
	client := &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 {
				return http.ErrUseLastResponse
			}
			// Re-validate after redirect
			if isBlockedHost(req.URL.Hostname()) {
				return fmt.Errorf("redirect to private host blocked")
			}
			return nil
		},
	}

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		http.Error(w, "failed to create request", http.StatusInternalServerError)
		return
	}

	// Forward Range header if present (enables resumable downloads)
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SaveIt-Proxy/1.0)")
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "upstream fetch failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// ── Copy relevant upstream headers ────────────────────────────────────────
	passthroughHeaders := []string{
		"Content-Type", "Content-Length", "Content-Range",
		"Accept-Ranges", "Last-Modified", "ETag",
	}
	for _, h := range passthroughHeaders {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}

	// Detect extension from content-type if filename has none
	if !strings.Contains(filename, ".") {
		filename += extensionForContentType(resp.Header.Get("Content-Type"))
	}

	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, sanitize(filename)))
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Robots-Tag", "noindex")

	w.WriteHeader(resp.StatusCode)

	// Stream with a size cap to avoid crushing the server
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxProxyBytes))
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// isBlockedHost prevents SSRF to private / loopback / link-local addresses.
func isBlockedHost(host string) bool {
	// Strip port
	h := host
	if idx := strings.LastIndex(h, ":"); idx > strings.LastIndex(h, "]") {
		h = h[:idx]
	}
	h = strings.Trim(h, "[]") // unwrap IPv6

	// Resolve to IP
	addrs, err := net.LookupHost(h)
	if err != nil {
		// Can't resolve → block to be safe
		return true
	}

	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			return true
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return true
		}
	}
	return false
}

func extensionForContentType(ct string) string {
	ct = strings.ToLower(strings.Split(ct, ";")[0])
	switch ct {
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav":
		return ".wav"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ""
	}
}

func sanitize(name string) string {
	r := strings.NewReplacer(
		`"`, "", `\`, "", "\n", "", "\r", "", "/", "-",
	)
	s := r.Replace(name)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func proxyCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Range")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Content-Disposition")
}
