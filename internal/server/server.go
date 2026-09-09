package server

import (
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/yoldar/safe-social-proxy/internal/config"
	"github.com/yoldar/safe-social-proxy/internal/proxy"
	"github.com/yoldar/safe-social-proxy/internal/youtube"
	"github.com/yoldar/safe-social-proxy/web"
)

type Server struct {
	cfg     *config.Config
	reg     *proxy.Registry
	logger  *slog.Logger
	portal  *template.Template
	blocked *template.Template
	ytTmpl  *template.Template
	yt      *youtube.Client
}

func New(cfg *config.Config, reg *proxy.Registry, yt *youtube.Client, logger *slog.Logger) (*Server, error) {
	portal, err := template.ParseFS(web.FS, "portal.html")
	if err != nil {
		return nil, err
	}
	blocked, err := template.ParseFS(web.FS, "blocked.html")
	if err != nil {
		return nil, err
	}
	ytTmpl, err := template.ParseFS(web.FS, "yt.html")
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg: cfg, reg: reg, logger: logger,
		portal: portal, blocked: blocked, ytTmpl: ytTmpl, yt: yt,
	}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	s.route(sw, r)
	s.logger.Info("req",
		"host", r.Host, "method", r.Method, "path", r.URL.Path,
		"status", sw.status, "dur", time.Since(start).Round(time.Millisecond))
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok"))
		return
	case "/robots.txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("User-agent: *\nDisallow: /\n"))
		return
	}

	host := strings.ToLower(r.Host)
	if host == s.cfg.PublicDomain {
		s.servePortal(w, r)
		return
	}
	if t, ok := s.reg.Hosts[host]; ok {
		s.serveSite(w, r, t)
		return
	}
	http.Redirect(w, r, s.cfg.PublicURL(""), http.StatusFound)
}

func (s *Server) serveSite(w http.ResponseWriter, r *http.Request, t *proxy.Target) {
	rt := t.RT
	path := r.URL.Path
	if strings.HasPrefix(path, proxy.StreamPrefix) {
		proxy.ServeStream(w, r, rt, s.reg.Transport, s.logger)
		return
	}
	if rt.CheckBlocked(path) {
		s.renderBlocked(w, rt)
		return
	}
	if rt.Site.Handler == "youtube" {
		s.serveYouTube(w, r, rt)
		return
	}
	// Root redirect applies to the site's main subdomain only, not CDN hosts.
	if rt.Site.RootRedirect != "" && path == "/" && t.UpstreamHost == rt.Site.Upstream {
		http.Redirect(w, r, rt.Site.RootRedirect, http.StatusFound)
		return
	}
	if rt.CheckBlockedAPI(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("{}"))
		return
	}
	t.Proxy.ServeHTTP(w, r)
}

type portalSite struct {
	Label, Note, Icon, URL string
}

func (s *Server) servePortal(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	var sites []portalSite
	for _, rt := range s.reg.Sites {
		icon := rt.Site.Icon
		if icon == "" {
			icon = "🌐"
		}
		sites = append(sites, portalSite{
			Label: rt.Site.Label,
			Note:  rt.Site.Note,
			Icon:  icon,
			URL:   s.cfg.PublicURL(rt.Site.Subdomain),
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.portal.Execute(w, map[string]any{"Sites": sites}); err != nil {
		s.logger.Error("portal render", "err", err)
	}
}

func (s *Server) renderBlocked(w http.ResponseWriter, rt *proxy.SiteRuntime) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	err := s.blocked.Execute(w, map[string]any{
		"SiteLabel": rt.Site.Label,
		"PortalURL": s.cfg.PublicURL(""),
	})
	if err != nil {
		s.logger.Error("blocked render", "err", err)
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// Flush keeps streaming working through the status-capturing wrapper.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
