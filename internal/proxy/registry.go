package proxy

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"
	"time"

	"github.com/yoldar/safe-social-proxy/internal/config"
	"github.com/yoldar/safe-social-proxy/internal/rewrite"
)

type apiRule struct {
	re           *regexp.Regexp
	bodyContains string
}

// SiteRuntime is a site's compiled rewriting/blocking state, shared by every
// proxy host (subdomain) belonging to that site.
type SiteRuntime struct {
	Site        *config.Site
	Cfg         *config.Config
	PrimaryHost string // <subdomain>.<public_domain>, incl. port when local

	BlockedRes      []*regexp.Regexp
	APIRules        []apiRule
	Replacer        *strings.Replacer
	Wildcards       []rewrite.WildcardRule
	JSONFilters     []rewrite.BodyFilter
	ProxyToUpstream map[string]string
	UpstreamToProxy map[string]string
	AllowedSuffixes []string
}

// Target is one proxy host: a subdomain bound to a single upstream host.
type Target struct {
	RT           *SiteRuntime
	UpstreamHost string
	Proxy        *httputil.ReverseProxy
}

type Registry struct {
	Cfg       *config.Config
	Hosts     map[string]*Target
	Sites     []*SiteRuntime
	Transport *http.Transport
}

func NewRegistry(cfg *config.Config, logger *slog.Logger) (*Registry, error) {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	reg := &Registry{
		Cfg:       cfg,
		Hosts:     map[string]*Target{},
		Transport: transport,
	}
	hostFor := func(sub string) string { return sub + "." + cfg.PublicDomain }

	for _, s := range cfg.Sites {
		if !s.Enabled {
			continue
		}
		rt := &SiteRuntime{
			Site:        s,
			Cfg:         cfg,
			PrimaryHost: hostFor(s.Subdomain),
		}
		pairs := map[string]string{s.Upstream: rt.PrimaryHost}
		for up, sub := range s.HostMap {
			pairs[strings.ToLower(up)] = hostFor(sub)
		}
		rt.UpstreamToProxy = pairs
		rt.ProxyToUpstream = map[string]string{}
		for up, p := range pairs {
			rt.ProxyToUpstream[p] = up
		}
		rt.Replacer = rewrite.NewHostReplacer(pairs, cfg.PublicScheme)
		for _, suf := range s.WildcardSuffixes {
			suf = strings.ToLower(suf)
			rt.Wildcards = append(rt.Wildcards,
				rewrite.NewWildcardRules(suf, cfg.PublicScheme, rt.PrimaryHost)...)
			rt.AllowedSuffixes = append(rt.AllowedSuffixes, suf)
		}
		for _, expr := range s.BlockedPaths {
			rt.BlockedRes = append(rt.BlockedRes, regexp.MustCompile(expr))
		}
		for _, r := range s.BlockedAPI {
			rt.APIRules = append(rt.APIRules, apiRule{
				re:           regexp.MustCompile(r.Path),
				bodyContains: r.BodyContains,
			})
		}
		for _, name := range s.JSONFilters {
			f, ok := rewrite.Filters[name]
			if !ok {
				return nil, fmt.Errorf("site %q: unknown json filter %q", s.Name, name)
			}
			rt.JSONFilters = append(rt.JSONFilters, f)
		}
		reg.Sites = append(reg.Sites, rt)

		for up, proxyHost := range pairs {
			t := &Target{RT: rt, UpstreamHost: up}
			t.Proxy = newReverseProxy(t, transport, logger)
			reg.Hosts[proxyHost] = t
		}
	}
	return reg, nil
}
