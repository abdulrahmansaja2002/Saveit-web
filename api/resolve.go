// api/resolve.go — POST /api/resolve
// Resolves a social-media page URL into direct download link(s)
// using the cobalt.tools API as the extraction backend.
package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// ── Request / Response types ─────────────────────────────────────────────────

type ResolveRequest struct {
	URL       string `json:"url"`
	Quality   string `json:"quality"`   // max | 4320 | 2160 | 1440 | 1080 | 720 | 480 | 360 | 144
	AudioOnly bool   `json:"audioOnly"` // audio-only (MP3)
	MuteVideo bool   `json:"muteVideo"` // video without audio
}

type ResolveResponse struct {
	Status   string       `json:"status"`             // direct | picker | error
	Platform string       `json:"platform"`
	URL      string       `json:"url,omitempty"`
	Filename string       `json:"filename,omitempty"`
	Picker   []PickerItem `json:"picker,omitempty"`
	Audio    string       `json:"audio,omitempty"` // background audio for picker
	Error    string       `json:"error,omitempty"`
}

type PickerItem struct {
	Type  string `json:"type"`            // photo | video
	URL   string `json:"url"`
	Thumb string `json:"thumb,omitempty"`
}

// ── cobalt.tools wire types ───────────────────────────────────────────────────

type cobaltReq struct {
	URL           string `json:"url"`
	VideoQuality  string `json:"videoQuality"`
	DownloadMode  string `json:"downloadMode"`  // auto | audio | mute
	AudioFormat   string `json:"audioFormat"`   // best | mp3 | ogg | wav | opus
	FilenameStyle string `json:"filenameStyle"` // pretty | classic | basic | nerdy
}

type cobaltResp struct {
	Status     string       `json:"status"` // redirect | stream | picker | error
	URL        string       `json:"url"`
	Filename   string       `json:"filename"`
	PickerType string       `json:"pickerType"`
	Picker     []cobaltItem `json:"picker"`
	Audio      string       `json:"audio"`
	Error      *cobaltErr   `json:"error"`
}

type cobaltItem struct {
	Type  string `json:"type"`
	URL   string `json:"url"`
	Thumb string `json:"thumb"`
}

type cobaltErr struct {
	Code    string      `json:"code"`
	Context interface{} `json:"context"`
}

// ── Platform detection ────────────────────────────────────────────────────────

type platformInfo struct {
	Name  string
	Color string
}

var platforms = []struct {
	Re   *regexp.Regexp
	Info platformInfo
}{
	{regexp.MustCompile(`(?i)(youtube\.com|youtu\.be)`), platformInfo{"YouTube", "#FF0033"}},
	{regexp.MustCompile(`(?i)instagram\.com`), platformInfo{"Instagram", "#E1306C"}},
	{regexp.MustCompile(`(?i)tiktok\.com`), platformInfo{"TikTok", "#69C9D0"}},
	{regexp.MustCompile(`(?i)(twitter\.com|x\.com)`), platformInfo{"Twitter / X", "#1DA1F2"}},
	{regexp.MustCompile(`(?i)(facebook\.com|fb\.watch)`), platformInfo{"Facebook", "#1877F2"}},
	{regexp.MustCompile(`(?i)reddit\.com`), platformInfo{"Reddit", "#FF4500"}},
	{regexp.MustCompile(`(?i)pinterest\.(com|co\.uk)`), platformInfo{"Pinterest", "#E60023"}},
	{regexp.MustCompile(`(?i)twitch\.tv`), platformInfo{"Twitch", "#9146FF"}},
	{regexp.MustCompile(`(?i)vimeo\.com`), platformInfo{"Vimeo", "#1AB7EA"}},
	{regexp.MustCompile(`(?i)soundcloud\.com`), platformInfo{"SoundCloud", "#FF7700"}},
	{regexp.MustCompile(`(?i)dailymotion\.com`), platformInfo{"Dailymotion", "#0066DC"}},
	{regexp.MustCompile(`(?i)bilibili\.(com|tv)`), platformInfo{"Bilibili", "#00A1D6"}},
}

func detectPlatform(rawURL string) string {
	for _, p := range platforms {
		if p.Re.MatchString(rawURL) {
			return p.Info.Name
		}
	}
	return "Unknown"
}

// ── Handler ───────────────────────────────────────────────────────────────────

func ResolveHandler(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Parse body (cap at 4 KB)
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		apiError(w, http.StatusBadRequest, "could not read body")
		return
	}
	defer r.Body.Close()

	var req ResolveRequest
	if err := json.Unmarshal(body, &req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		apiError(w, http.StatusBadRequest, "url is required")
		return
	}

	// Basic URL validation
	parsed, err := url.ParseRequestURI(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		apiError(w, http.StatusBadRequest, "invalid url — must start with http:// or https://")
		return
	}

	quality := req.Quality
	if quality == "" {
		quality = "1080"
	}

	mode := "auto"
	if req.AudioOnly {
		mode = "audio"
	} else if req.MuteVideo {
		mode = "mute"
	}

	cobaltBase := os.Getenv("COBALT_API")
	if cobaltBase == "" {
		cobaltBase = "https://api.cobalt.tools"
	}

	cr, err := callCobalt(cobaltBase, req.URL, quality, mode)
	if err != nil {
		apiError(w, http.StatusBadGateway, fmt.Sprintf("resolution failed: %s", err.Error()))
		return
	}

	resp := buildResponse(cr, detectPlatform(req.URL))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ── cobalt.tools call ─────────────────────────────────────────────────────────

func callCobalt(base, mediaURL, quality, mode string) (*cobaltResp, error) {
	payload := cobaltReq{
		URL:           mediaURL,
		VideoQuality:  quality,
		DownloadMode:  mode,
		AudioFormat:   "mp3",
		FilenameStyle: "pretty",
	}

	b, _ := json.Marshal(payload)

	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodPost, base+"/", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "SaveIt/1.0 (+https://github.com)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	var cr cobaltResp
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return nil, fmt.Errorf("invalid response from resolver")
	}

	if cr.Status == "error" || cr.Status == "rate-limit" {
		code := "unknown_error"
		if cr.Error != nil {
			code = cr.Error.Code
		}
		return nil, fmt.Errorf("cobalt: %s", humanCobaltError(code))
	}

	return &cr, nil
}

func buildResponse(cr *cobaltResp, platform string) ResolveResponse {
	resp := ResolveResponse{Platform: platform}

	switch cr.Status {
	case "redirect", "stream", "tunnel":
		resp.Status = "direct"
		resp.URL = cr.URL
		resp.Filename = cr.Filename
	case "picker":
		resp.Status = "picker"
		resp.Audio = cr.Audio
		for _, item := range cr.Picker {
			resp.Picker = append(resp.Picker, PickerItem{
				Type:  item.Type,
				URL:   item.URL,
				Thumb: item.Thumb,
			})
		}
	default:
		resp.Status = "error"
		resp.Error = "unexpected status from resolver: " + cr.Status
	}

	return resp
}

func humanCobaltError(code string) string {
	switch {
	case strings.Contains(code, "unsupported"):
		return "this URL is not supported"
	case strings.Contains(code, "private"):
		return "this content is private"
	case strings.Contains(code, "age"):
		return "age-restricted content"
	case strings.Contains(code, "limit"):
		return "rate limited — try again in a moment"
	case strings.Contains(code, "content.too_long"):
		return "content is too long to process"
	default:
		return code
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func apiError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
