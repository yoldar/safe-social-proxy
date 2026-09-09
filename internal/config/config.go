package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// BlockedAPIRule blocks proxied API requests whose path matches Path and,
// when BodyContains is set, whose request body contains that substring.
type BlockedAPIRule struct {
	Path         string `yaml:"path"`
	BodyContains string `yaml:"body_contains"`
}

type Site struct {
	Name      string `yaml:"name"`
	Label     string `yaml:"label"`
	Note      string `yaml:"note"`
	Icon      string `yaml:"icon"`
	Subdomain string `yaml:"subdomain"`
	Upstream  string `yaml:"upstream"`
	Enabled   bool   `yaml:"enabled"`
	// Handler selects a built-in frontend instead of the generic reverse
	// proxy. Supported: "" (generic) and "youtube" (own search+watch UI;
	// the real YouTube app can't play video through a rewriting proxy —
	// playback needs an origin-bound PoToken attestation).
	Handler string `yaml:"handler"`

	// HostMap: extra upstream hosts served through their own subdomain.
	HostMap map[string]string `yaml:"host_map"`
	// WildcardSuffixes: upstream host suffixes with unbounded host sets
	// (CDNs); rewritten to the /__proxy/h/<host>/ stream endpoint.
	WildcardSuffixes []string `yaml:"wildcard_suffixes"`

	BlockedPaths []string          `yaml:"blocked_paths"`
	BlockedAPI   []BlockedAPIRule  `yaml:"blocked_api"`
	RootRedirect string            `yaml:"root_redirect"`
	RewriteTypes []string          `yaml:"rewrite_types"`
	JSONFilters  []string          `yaml:"json_filters"`
	InjectCSS    string            `yaml:"inject_css"`
	ForceUA      string            `yaml:"force_user_agent"`
	ExtraCookies map[string]string `yaml:"extra_cookies"`
}

type Config struct {
	PublicDomain   string  `yaml:"public_domain"`
	PublicScheme   string  `yaml:"public_scheme"`
	Listen         string  `yaml:"listen"`
	MaxRewriteBody int64   `yaml:"max_rewrite_body"`
	Sites          []*Site `yaml:"sites"`
}

var DefaultRewriteTypes = []string{
	"text/html", "application/json", "text/javascript",
	"application/javascript", "application/x-javascript", "text/css",
}

var subdomainRe = regexp.MustCompile(`^[a-z0-9-]+$`)

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if v := os.Getenv("PROXY_DOMAIN"); v != "" {
		c.PublicDomain = v
	}
	if v := os.Getenv("PROXY_SCHEME"); v != "" {
		c.PublicScheme = v
	}
	if v := os.Getenv("PROXY_LISTEN"); v != "" {
		c.Listen = v
	}

	if c.PublicDomain == "" {
		return nil, fmt.Errorf("public_domain is required")
	}
	c.PublicDomain = strings.ToLower(c.PublicDomain)
	if c.PublicScheme == "" {
		c.PublicScheme = "https"
	}
	if c.PublicScheme != "http" && c.PublicScheme != "https" {
		return nil, fmt.Errorf("public_scheme must be http or https, got %q", c.PublicScheme)
	}
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.MaxRewriteBody <= 0 {
		c.MaxRewriteBody = 5 << 20 // 5 MiB
	}

	seen := map[string]string{} // subdomain -> site name
	for _, s := range c.Sites {
		if s.Name == "" || s.Subdomain == "" || s.Upstream == "" {
			return nil, fmt.Errorf("site %q: name, subdomain and upstream are required", s.Name)
		}
		if s.Label == "" {
			s.Label = s.Name
		}
		if s.Handler != "" && s.Handler != "youtube" {
			return nil, fmt.Errorf("site %q: unknown handler %q", s.Name, s.Handler)
		}
		if len(s.RewriteTypes) == 0 {
			s.RewriteTypes = DefaultRewriteTypes
		}
		subs := []string{s.Subdomain}
		for _, sub := range s.HostMap {
			subs = append(subs, sub)
		}
		for _, sub := range subs {
			if !subdomainRe.MatchString(sub) {
				return nil, fmt.Errorf("site %q: invalid subdomain %q", s.Name, sub)
			}
			if prev, dup := seen[sub]; dup {
				return nil, fmt.Errorf("subdomain %q used by both %q and %q", sub, prev, s.Name)
			}
			seen[sub] = s.Name
		}
		for _, re := range s.BlockedPaths {
			if _, err := regexp.Compile(re); err != nil {
				return nil, fmt.Errorf("site %q: bad blocked_paths regex %q: %w", s.Name, re, err)
			}
		}
		for _, r := range s.BlockedAPI {
			if _, err := regexp.Compile(r.Path); err != nil {
				return nil, fmt.Errorf("site %q: bad blocked_api path regex %q: %w", s.Name, r.Path, err)
			}
		}
		for _, suf := range s.WildcardSuffixes {
			if !strings.HasPrefix(suf, ".") {
				return nil, fmt.Errorf("site %q: wildcard suffix %q must start with '.'", s.Name, suf)
			}
		}
	}
	return &c, nil
}

// PublicURL returns the browser-facing URL for a subdomain ("" = portal).
func (c *Config) PublicURL(sub string) string {
	host := c.PublicDomain
	if sub != "" {
		host = sub + "." + host
	}
	return c.PublicScheme + "://" + host
}
