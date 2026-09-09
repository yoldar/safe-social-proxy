package rewrite

import "encoding/json"

// BodyFilter transforms a JSON response body; it must return the input
// unchanged on any parse problem (fail open — a broken page is worse than a
// visible Shorts shelf, which URL blocking and CSS still cover).
type BodyFilter func([]byte) []byte

var Filters = map[string]BodyFilter{
	"yt_remove_shorts_shelves": ytRemoveShortsShelves,
}

// Renderer keys that identify Shorts-only content in YouTube's innertube
// JSON (both www and m web clients).
var ytKillKeys = map[string]bool{
	"reelShelfRenderer":     true,
	"reelItemRenderer":      true,
	"shortsLockupViewModel": true,
	"reelWatchEndpoint":     true,
}

func ytRemoveShortsShelves(b []byte) []byte {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return b
	}
	v = prune(v)
	out, err := json.Marshal(v)
	if err != nil {
		return b
	}
	return out
}

// prune removes array elements that contain a kill key within a few levels
// (shelves are usually wrapped, e.g. richSectionRenderer > reelShelfRenderer).
func prune(v any) any {
	switch t := v.(type) {
	case []any:
		out := t[:0]
		for _, e := range t {
			if containsKill(e, 4) {
				continue
			}
			out = append(out, prune(e))
		}
		return out
	case map[string]any:
		for k, e := range t {
			t[k] = prune(e)
		}
		return t
	default:
		return v
	}
}

func containsKill(v any, depth int) bool {
	if depth == 0 {
		return false
	}
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			if ytKillKeys[k] {
				return true
			}
			if containsKill(e, depth-1) {
				return true
			}
		}
	case []any:
		for _, e := range t {
			if containsKill(e, depth-1) {
				return true
			}
		}
	}
	return false
}
