// Package youtube is a minimal server-rendered YouTube frontend (search +
// watch) backed by yt-dlp for stream extraction. The real YouTube web app
// cannot work through a rewriting proxy anymore: playback requires a BotGuard
// attestation (PoToken) bound to youtube.com's origin, so we own the UI and
// let yt-dlp's maintained extractor do the heavy lifting.
package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

type Client struct {
	Bin     string
	sem     chan struct{}
	mu      sync.Mutex
	videos  map[string]*Video
	queries map[string]*searchEntry
}

type Video struct {
	ID        string
	Title     string
	Author    string
	Duration  int
	StreamURL string // upstream googlevideo URL (proxy it before serving!)
	Kind      string // "hls" (master manifest) or "mp4" (muxed progressive)
	Height    int
	fetched   time.Time
}

type SearchItem struct {
	ID       string
	Title    string
	Author   string
	Duration string
	Views    string
}

type searchEntry struct {
	items   []SearchItem
	fetched time.Time
}

const (
	videoTTL  = 30 * time.Minute // googlevideo URLs live ~6h; refresh well before
	searchTTL = 10 * time.Minute
	execLimit = 45 * time.Second
)

func New() *Client {
	bin := os.Getenv("YTDLP_PATH")
	if bin == "" {
		bin = "yt-dlp"
	}
	return &Client{
		Bin:     bin,
		sem:     make(chan struct{}, 3),
		videos:  map[string]*Video{},
		queries: map[string]*searchEntry{},
	}
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, execLimit)
	defer cancel()
	base := []string{"--no-warnings", "--no-playlist", "--no-progress", "-J"}
	cmd := exec.CommandContext(ctx, c.Bin, append(base, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			msg := string(ee.Stderr)
			if len(msg) > 300 {
				msg = msg[len(msg)-300:]
			}
			return nil, fmt.Errorf("yt-dlp: %s", strings.TrimSpace(msg))
		}
		return nil, err
	}
	return out, nil
}

// Search runs a flat ytsearch and caches the result briefly.
func (c *Client) Search(ctx context.Context, q string) ([]SearchItem, error) {
	key := strings.ToLower(strings.TrimSpace(q))
	c.mu.Lock()
	if e, ok := c.queries[key]; ok && time.Since(e.fetched) < searchTTL {
		c.mu.Unlock()
		return e.items, nil
	}
	c.mu.Unlock()

	out, err := c.run(ctx, "--flat-playlist", "ytsearch24:"+q)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Entries []struct {
			ID        string  `json:"id"`
			Title     string  `json:"title"`
			Uploader  string  `json:"uploader"`
			Channel   string  `json:"channel"`
			Duration  float64 `json:"duration"`
			ViewCount float64 `json:"view_count"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parse search: %w", err)
	}
	items := make([]SearchItem, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		if e.ID == "" {
			continue
		}
		author := e.Channel
		if author == "" {
			author = e.Uploader
		}
		items = append(items, SearchItem{
			ID:       e.ID,
			Title:    e.Title,
			Author:   author,
			Duration: fmtDuration(int(e.Duration)),
			Views:    fmtViews(int64(e.ViewCount)),
		})
	}
	c.mu.Lock()
	c.queries[key] = &searchEntry{items: items, fetched: time.Now()}
	if len(c.queries) > 200 {
		c.evictQueriesLocked()
	}
	c.mu.Unlock()
	return items, nil
}

// Resolve extracts a playable muxed MP4 stream URL for one video.
func (c *Client) Resolve(ctx context.Context, id string) (*Video, error) {
	c.mu.Lock()
	if v, ok := c.videos[id]; ok && time.Since(v.fetched) < videoTTL {
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()

	out, err := c.run(ctx, "https://www.youtube.com/watch?v="+url.QueryEscape(id))
	if err != nil {
		return nil, err
	}
	var resp struct {
		ID       string  `json:"id"`
		Title    string  `json:"title"`
		Channel  string  `json:"channel"`
		Uploader string  `json:"uploader"`
		Duration float64 `json:"duration"`
		Formats  []struct {
			URL         string  `json:"url"`
			ManifestURL string  `json:"manifest_url"`
			Ext         string  `json:"ext"`
			Vcodec      string  `json:"vcodec"`
			Acodec      string  `json:"acodec"`
			Height      float64 `json:"height"`
			Protocol    string  `json:"protocol"`
		} `json:"formats"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parse video: %w", err)
	}
	// Preferred: the HLS master manifest (hls.js plays it with separate
	// audio renditions). Muxed progressive MP4 barely exists on YouTube
	// anymore, but keep it as a fallback for other cases.
	var streamURL, kind string
	var height int
	for _, f := range resp.Formats {
		if f.ManifestURL != "" && strings.HasPrefix(f.Protocol, "m3u8") {
			streamURL, kind = f.ManifestURL, "hls"
			if int(f.Height) > height {
				height = int(f.Height)
			}
		}
	}
	if streamURL == "" {
		type cand struct {
			url    string
			height int
		}
		var cands []cand
		for _, f := range resp.Formats {
			if f.URL == "" || f.Vcodec == "none" || f.Vcodec == "" || f.Acodec == "none" || f.Acodec == "" {
				continue
			}
			if !strings.HasPrefix(f.Protocol, "http") || (f.Ext != "mp4" && f.Ext != "webm") {
				continue
			}
			cands = append(cands, cand{f.URL, int(f.Height)})
		}
		if len(cands) == 0 {
			return nil, fmt.Errorf("no playable format for %s", id)
		}
		sort.Slice(cands, func(i, j int) bool { return cands[i].height > cands[j].height })
		streamURL, kind, height = cands[0].url, "mp4", cands[0].height
	}
	author := resp.Channel
	if author == "" {
		author = resp.Uploader
	}
	v := &Video{
		ID:        resp.ID,
		Title:     resp.Title,
		Author:    author,
		Duration:  int(resp.Duration),
		StreamURL: streamURL,
		Kind:      kind,
		Height:    height,
		fetched:   time.Now(),
	}
	c.mu.Lock()
	c.videos[id] = v
	if len(c.videos) > 300 {
		c.evictVideosLocked()
	}
	c.mu.Unlock()
	return v, nil
}

func (c *Client) evictQueriesLocked() {
	for k, e := range c.queries {
		if time.Since(e.fetched) > searchTTL {
			delete(c.queries, k)
		}
	}
}

func (c *Client) evictVideosLocked() {
	for k, v := range c.videos {
		if time.Since(v.fetched) > videoTTL {
			delete(c.videos, k)
		}
	}
}

func fmtDuration(sec int) string {
	if sec <= 0 {
		return ""
	}
	h, m, s := sec/3600, sec%3600/60, sec%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func fmtViews(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1f млрд", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1f млн", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0f тыс.", float64(n)/1e3)
	case n <= 0:
		return ""
	default:
		return fmt.Sprintf("%d", n)
	}
}
