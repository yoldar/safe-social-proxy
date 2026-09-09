package server

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/yoldar/safe-social-proxy/internal/proxy"
	"github.com/yoldar/safe-social-proxy/internal/youtube"
)

var videoIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{6,20}$`)

type ytPageData struct {
	Page  string
	Query string
	Error string
	Items []youtube.SearchItem
	Video *ytVideoView
	Title string
}

type ytVideoView struct {
	ID, Title, Author, Src, Kind string
	Height                       int
}

func (s *Server) serveYouTube(w http.ResponseWriter, r *http.Request, rt *proxy.SiteRuntime) {
	data := ytPageData{Page: "home"}
	switch {
	case r.URL.Path == "/" || r.URL.Path == "":
		// home: just the search form
	case r.URL.Path == "/results" || r.URL.Path == "/search":
		q := r.URL.Query().Get("search_query")
		if q == "" {
			q = r.URL.Query().Get("q")
		}
		q = strings.TrimSpace(q)
		if q == "" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		data.Page, data.Query, data.Title = "results", q, q
		items, err := s.yt.Search(r.Context(), q)
		if err != nil {
			s.logger.Warn("yt search", "q", q, "err", err)
			data.Error = err.Error()
		}
		data.Items = items
	case r.URL.Path == "/watch":
		id := r.URL.Query().Get("v")
		if !videoIDRe.MatchString(id) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		data.Page = "watch"
		v, err := s.yt.Resolve(r.Context(), id)
		if err != nil {
			s.logger.Warn("yt resolve", "id", id, "err", err)
			data.Error = err.Error()
		} else {
			src, ok := streamProxyPath(v.StreamURL, rt)
			if !ok {
				data.Error = "поток указывает на неизвестный хост"
			} else {
				data.Video = &ytVideoView{
					ID: v.ID, Title: v.Title, Author: v.Author,
					Src: src, Kind: v.Kind, Height: v.Height,
				}
				data.Title = v.Title
			}
		}
	default:
		// Any other YouTube URL (channels, playlists, shared links):
		// try to salvage a video id, otherwise go home.
		if id := r.URL.Query().Get("v"); videoIDRe.MatchString(id) {
			http.Redirect(w, r, "/watch?v="+url.QueryEscape(id), http.StatusFound)
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.ytTmpl.Execute(w, data); err != nil {
		s.logger.Error("yt render", "err", err)
	}
}

// streamProxyPath converts an upstream CDN URL into the same-origin
// /__proxy/h/ form, refusing hosts outside the site's allowlist.
func streamProxyPath(raw string, rt *proxy.SiteRuntime) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	allowed := false
	for _, suf := range rt.AllowedSuffixes {
		if strings.HasSuffix(u.Host, suf) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", false
	}
	p := proxy.StreamPrefix + u.Host + u.EscapedPath()
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p, true
}
