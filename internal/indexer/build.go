package indexer

import (
	"fmt"
	"net/url"
	"strings"
)

// Feed formats a configured site can use.
const (
	KindNyaa       = "nyaa"
	KindTokyoTosho = "tokyotosho"
)

// Build makes a source for one configured site. An adult Nyaa-style site uses
// the sister site's category numbering.
func Build(kind, base string, adult bool) (Source, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("indexer %q: url %q is not a full URL", kind, base)
	}
	switch strings.ToLower(kind) {
	case KindNyaa:
		if adult {
			return NewSukebei(base), nil
		}
		return NewNyaa(base), nil
	case KindTokyoTosho:
		return NewTokyoTosho(base), nil
	}
	return nil, fmt.Errorf("indexer type %q: want %q or %q", kind, KindNyaa, KindTokyoTosho)
}
