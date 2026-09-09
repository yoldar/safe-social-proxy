package rewrite

import (
	"net/http"
	"strings"
)

// Headers removed from upstream responses: they either pin the upstream
// origin (CSP, CORP) or enforce policies that break a rewriting proxy.
var strippedResponseHeaders = []string{
	"Content-Security-Policy",
	"Content-Security-Policy-Report-Only",
	"X-Frame-Options",
	"Strict-Transport-Security",
	"Cross-Origin-Opener-Policy",
	"Cross-Origin-Embedder-Policy",
	"Cross-Origin-Resource-Policy",
	"Origin-Agent-Cluster",
	"Report-To",
	"Reporting-Endpoints",
	"NEL",
	"Public-Key-Pins",
}

func StripSecurityHeaders(h http.Header) {
	for _, name := range strippedResponseHeaders {
		h.Del(name)
	}
}

// RewriteSetCookies drops the Domain attribute so cookies scope to the proxy
// subdomain, and drops Secure/SameSite=None when serving over plain http
// (browsers reject SameSite=None without Secure).
func RewriteSetCookies(h http.Header, httpsPublic bool) {
	cookies := h.Values("Set-Cookie")
	if len(cookies) == 0 {
		return
	}
	out := make([]string, 0, len(cookies))
	for _, c := range cookies {
		parts := strings.Split(c, ";")
		kept := parts[:0]
		for i, p := range parts {
			t := strings.TrimSpace(p)
			lower := strings.ToLower(t)
			if i > 0 {
				if strings.HasPrefix(lower, "domain=") {
					continue
				}
				if !httpsPublic && (lower == "secure" || lower == "samesite=none") {
					continue
				}
			}
			kept = append(kept, p)
		}
		out = append(out, strings.Join(kept, ";"))
	}
	h.Del("Set-Cookie")
	for _, c := range out {
		h.Add("Set-Cookie", c)
	}
}

// RewriteRequestOriginHeaders maps proxy hosts in Referer/Origin back to the
// upstream hosts (some CDNs validate them). proxyToUpstream maps
// proxy host[:port] -> upstream host.
func RewriteRequestOriginHeaders(h http.Header, proxyToUpstream map[string]string) {
	for _, name := range []string{"Referer", "Origin"} {
		v := h.Get(name)
		if v == "" || v == "null" {
			continue
		}
		for p, up := range proxyToUpstream {
			for _, scheme := range []string{"http://", "https://"} {
				prefix := scheme + p
				if strings.HasPrefix(v, prefix) && hostBoundary(v, len(prefix)) {
					h.Set(name, "https://"+up+v[len(prefix):])
					goto next
				}
			}
		}
	next:
	}
}

// hostBoundary reports whether v[n:] starts at a host boundary (end, path,
// query or port) so "yt.x.com" does not match inside "yt.x.company.com".
func hostBoundary(v string, n int) bool {
	if len(v) == n {
		return true
	}
	switch v[n] {
	case '/', '?', '#', ':':
		return true
	}
	return false
}
