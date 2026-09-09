package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/yoldar/safe-social-proxy/internal/rewrite"
)

// StreamPrefix routes wildcard-host upstream fetches (CDN media, thumbnails)
// that cannot get a dedicated subdomain: /__proxy/h/<host>/<original path>.
const StreamPrefix = "/__proxy/h/"

// Request headers forwarded to the CDN. Range is the critical one (seeking).
var streamRequestHeaders = []string{
	"Range", "Accept", "Accept-Language", "User-Agent",
	"If-None-Match", "If-Modified-Since", "Content-Type",
}

// ServeStream proxies one request to an allowlisted wildcard host, streaming
// the body without buffering.
func ServeStream(w http.ResponseWriter, r *http.Request, rt *SiteRuntime, transport http.RoundTripper, logger *slog.Logger) {
	rest := strings.TrimPrefix(r.URL.EscapedPath(), StreamPrefix)
	host, path, _ := strings.Cut(rest, "/")
	host = strings.ToLower(host)
	if host == "" || strings.ContainsAny(host, ":@") || !allowedStreamHost(rt, host) {
		http.Error(w, "forbidden upstream host", http.StatusForbidden)
		return
	}
	target := "https://" + host + "/" + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, "bad stream url", http.StatusBadRequest)
		return
	}
	for _, name := range streamRequestHeaders {
		if v := r.Header.Get(name); v != "" {
			req.Header.Set(name, v)
		}
	}
	// CDNs validate the traffic source, not the viewer.
	req.Header.Set("Referer", "https://"+rt.Site.Upstream+"/")
	req.Header.Set("Origin", "https://"+rt.Site.Upstream)

	resp, err := transport.RoundTrip(req)
	if err != nil {
		logger.Warn("stream error", "site", rt.Site.Name, "host", host, "err", err)
		http.Error(w, "upstream stream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	hdr := w.Header()
	for name, vals := range resp.Header {
		if name == "Set-Cookie" {
			continue
		}
		for _, v := range vals {
			hdr.Add(name, v)
		}
	}
	rewrite.StripSecurityHeaders(hdr)
	if loc := hdr.Get("Location"); loc != "" {
		hdr.Set("Location", rewriteURL(rt, loc))
	}
	// HLS playlists reference absolute CDN URLs; rewrite them so the player
	// keeps fetching same-origin through this endpoint.
	if isM3U8(resp.Header.Get("Content-Type"), r.URL.Path) {
		reader, _ := rewrite.Decompress(resp.Body, resp.Header.Get("Content-Encoding"))
		body, err := io.ReadAll(io.LimitReader(reader, 8<<20))
		if err != nil {
			http.Error(w, "manifest read error", http.StatusBadGateway)
			return
		}
		out := rewriteURL(rt, string(body))
		hdr.Del("Content-Length")
		hdr.Del("Content-Encoding")
		w.WriteHeader(resp.StatusCode)
		io.WriteString(w, out)
		return
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		logger.Debug("stream copy interrupted", "host", host, "err", err)
	}
}

func isM3U8(contentType, path string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "mpegurl") || strings.HasSuffix(path, ".m3u8")
}

func allowedStreamHost(rt *SiteRuntime, host string) bool {
	for _, suf := range rt.AllowedSuffixes {
		if strings.HasSuffix(host, suf) {
			return true
		}
	}
	return false
}
