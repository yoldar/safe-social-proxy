package rewrite

import (
	"compress/gzip"
	"io"
	"regexp"
	"strings"

	"github.com/andybalholm/brotli"
)

// WildcardRule rewrites URLs pointing at an unbounded upstream host set
// (e.g. *.googlevideo.com) into the site's /__proxy/h/<host>/ endpoint.
type WildcardRule struct {
	Re   *regexp.Regexp
	Repl string
}

// NewWildcardRules builds plain and JSON-escaped rewrite rules for one
// upstream suffix, targeting proxyBase (scheme://sub.domain of the site).
func NewWildcardRules(suffix, publicScheme, proxyHost string) []WildcardRule {
	q := regexp.QuoteMeta(suffix)
	hostPat := `([A-Za-z0-9][A-Za-z0-9.-]*` + q + `)`
	return []WildcardRule{
		{
			Re:   regexp.MustCompile(`https?://` + hostPat),
			Repl: publicScheme + `://` + proxyHost + `/__proxy/h/$1`,
		},
		{
			// JSON-escaped form: https:\/\/host\/path
			Re:   regexp.MustCompile(`https?:\\/\\/` + hostPat),
			Repl: publicScheme + `:\/\/` + proxyHost + `\/__proxy\/h\/$1`,
		},
	}
}

// NewHostReplacer builds a single-pass replacer mapping upstream hosts to
// proxy hosts. pairs is upstreamHost -> proxyHost (host[:port]).
// Scheme-qualified forms come first so http->https downgrades rewrite too.
func NewHostReplacer(pairs map[string]string, publicScheme string) *strings.Replacer {
	var args []string
	esc := publicScheme + `:\/\/`
	for up, p := range pairs {
		args = append(args,
			"https://"+up, publicScheme+"://"+p,
			"http://"+up, publicScheme+"://"+p,
			`https:\/\/`+up, esc+p,
			`http:\/\/`+up, esc+p,
			"//"+up, "//"+p,
			up, p,
		)
	}
	return strings.NewReplacer(args...)
}

// Decompress wraps body according to Content-Encoding. Returns the reader
// and whether the encoding was recognized (identity counts as recognized).
func Decompress(body io.Reader, encoding string) (io.Reader, bool) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
		return body, true
	case "gzip":
		gr, err := gzip.NewReader(body)
		if err != nil {
			return body, false
		}
		return gr, true
	case "br":
		return brotli.NewReader(body), true
	default:
		return body, false
	}
}

var headCloseRe = regexp.MustCompile(`(?i)</head>`)

// InjectCSS inserts a style block before </head>, or appends when missing.
func InjectCSS(html, css string) string {
	if css == "" {
		return html
	}
	block := "<style>\n" + css + "\n</style>"
	if loc := headCloseRe.FindStringIndex(html); loc != nil {
		return html[:loc[0]] + block + html[loc[0]:]
	}
	return html + block
}
