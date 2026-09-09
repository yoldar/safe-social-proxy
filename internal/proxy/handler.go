package proxy

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"github.com/yoldar/safe-social-proxy/internal/rewrite"
)

const maxSniffedRequestBody = 64 << 10

func newReverseProxy(t *Target, transport http.RoundTripper, logger *slog.Logger) *httputil.ReverseProxy {
	rt := t.RT
	return &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: 100 * time.Millisecond,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = t.UpstreamHost
			pr.Out.Host = t.UpstreamHost
			h := pr.Out.Header
			// One decompressor in the rewrite path; upstreams all speak gzip.
			h.Set("Accept-Encoding", "gzip")
			if rt.Site.ForceUA != "" {
				h.Set("User-Agent", rt.Site.ForceUA)
			}
			rewrite.RewriteRequestOriginHeaders(h, rt.ProxyToUpstream)
			addExtraCookies(h, rt.Site.ExtraCookies)
			// Do not leak the proxy chain to the upstream.
			for _, name := range []string{
				"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host",
				"X-Forwarded-Port", "X-Forwarded-Server", "X-Real-Ip", "Forwarded",
			} {
				h.Del(name)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			return modifyResponse(resp, t)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Warn("upstream error",
				"site", rt.Site.Name, "upstream", t.UpstreamHost,
				"path", r.URL.Path, "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
		},
	}
}

func addExtraCookies(h http.Header, extra map[string]string) {
	if len(extra) == 0 {
		return
	}
	cookie := h.Get("Cookie")
	for name, val := range extra {
		if !strings.Contains(cookie, name+"=") {
			if cookie != "" {
				cookie += "; "
			}
			cookie += name + "=" + val
		}
	}
	h.Set("Cookie", cookie)
}

type readCloser struct {
	io.Reader
	io.Closer
}

func modifyResponse(resp *http.Response, t *Target) error {
	rt := t.RT
	hdr := resp.Header

	if loc := hdr.Get("Location"); loc != "" {
		hdr.Set("Location", rewriteURL(rt, loc))
	}
	rewrite.RewriteSetCookies(hdr, rt.Cfg.PublicScheme == "https")
	rewrite.StripSecurityHeaders(hdr)
	if v := hdr.Get("Access-Control-Allow-Origin"); v != "" && v != "*" {
		hdr.Set("Access-Control-Allow-Origin", rt.Replacer.Replace(v))
	}

	// Bodyless statuses (1xx/204/304): never touch the body or Content-Length.
	if resp.StatusCode < 200 || resp.StatusCode == http.StatusNoContent ||
		resp.StatusCode == http.StatusNotModified {
		return nil
	}
	ct := hdr.Get("Content-Type")
	if !shouldRewrite(rt, ct) {
		return nil
	}
	orig := resp.Body
	reader, known := rewrite.Decompress(orig, hdr.Get("Content-Encoding"))
	if !known {
		return nil // unknown encoding: pass through untouched
	}
	maxBody := rt.Cfg.MaxRewriteBody
	buf, err := io.ReadAll(io.LimitReader(reader, maxBody+1))
	if err != nil {
		orig.Close()
		return err
	}
	if int64(len(buf)) > maxBody {
		// Oversized text body: stream the rest through decompressed, unrewritten.
		hdr.Del("Content-Encoding")
		hdr.Del("Content-Length")
		resp.ContentLength = -1
		resp.Body = readCloser{io.MultiReader(bytes.NewReader(buf), reader), orig}
		return nil
	}
	orig.Close()

	body := rewriteURL(rt, string(buf))
	if strings.HasPrefix(ct, "text/html") {
		body = rewrite.InjectCSS(body, rt.Site.InjectCSS)
	}
	if strings.Contains(ct, "json") && len(rt.JSONFilters) > 0 {
		b := []byte(body)
		for _, f := range rt.JSONFilters {
			b = f(b)
		}
		body = string(b)
	}
	hdr.Del("Content-Encoding")
	hdr.Set("Content-Length", strconv.Itoa(len(body)))
	resp.ContentLength = int64(len(body))
	resp.Body = io.NopCloser(strings.NewReader(body))
	return nil
}

// rewriteURL maps upstream hosts (fixed and wildcard) to proxy form in any
// text: header values or whole bodies.
func rewriteURL(rt *SiteRuntime, s string) string {
	s = rt.Replacer.Replace(s)
	for _, w := range rt.Wildcards {
		s = w.Re.ReplaceAllString(s, w.Repl)
	}
	return s
}

func shouldRewrite(rt *SiteRuntime, contentType string) bool {
	for _, t := range rt.Site.RewriteTypes {
		if strings.HasPrefix(contentType, t) {
			return true
		}
	}
	return false
}

// CheckBlocked reports whether the request path is blocked for the site.
func (rt *SiteRuntime) CheckBlocked(path string) bool {
	for _, re := range rt.BlockedRes {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

// CheckBlockedAPI sniffs (and restores) the request body when a rule needs it.
func (rt *SiteRuntime) CheckBlockedAPI(r *http.Request) bool {
	for _, rule := range rt.APIRules {
		if !rule.re.MatchString(r.URL.Path) {
			continue
		}
		if rule.bodyContains == "" {
			return true
		}
		if r.Body == nil {
			continue
		}
		buf, _ := io.ReadAll(io.LimitReader(r.Body, maxSniffedRequestBody))
		r.Body = readCloser{io.MultiReader(bytes.NewReader(buf), r.Body), r.Body}
		if bytes.Contains(buf, []byte(rule.bodyContains)) {
			return true
		}
	}
	return false
}
