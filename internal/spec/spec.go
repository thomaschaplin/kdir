// Package spec parses and validates .kubectx files.
package spec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

// Spec is the parsed content of a .kubectx file.
type Spec struct {
	// Matchers. Exactly one of Context, ContextRegex or Server is required,
	// except that Server may be narrowed with ContextRegex or User.
	Context      string `json:"context,omitempty"`
	ContextRegex string `json:"contextRegex,omitempty"`
	Server       string `json:"server,omitempty"`
	User         string `json:"user,omitempty"`

	Namespace string `json:"namespace,omitempty"`
	Protect   bool   `json:"protect,omitempty"`

	re *regexp.Regexp
}

// Regexp returns the compiled ContextRegex, or nil.
func (s *Spec) Regexp() *regexp.Regexp { return s.re }

// Describe returns a short human description of the matcher, e.g.
// `context "staging-eu"`.
func (s *Spec) Describe() string {
	var parts []string
	switch {
	case s.Context != "":
		parts = append(parts, fmt.Sprintf("context %q", s.Context))
	case s.Server != "":
		parts = append(parts, fmt.Sprintf("server %q", s.Server))
	}
	if s.ContextRegex != "" {
		if s.Server != "" {
			parts = append(parts, fmt.Sprintf("with context matching /%s/", s.ContextRegex))
		} else {
			parts = append(parts, fmt.Sprintf("a context matching /%s/", s.ContextRegex))
		}
	}
	if s.User != "" {
		parts = append(parts, fmt.Sprintf("with user %q", s.User))
	}
	return strings.Join(parts, " ")
}

var knownKeys = []string{"context", "contextRegex", "server", "user", "namespace", "protect"}

// Parse parses and validates a .kubectx file. Unknown keys are returned as
// warnings rather than errors so that a typo is visible but not fatal.
func Parse(data []byte) (*Spec, []string, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil, errors.New("file is empty; set one of context, contextRegex or server")
	}
	// YAMLToJSONStrict rejects duplicate keys, which would otherwise silently
	// pick one of two conflicting matchers.
	js, err := yaml.YAMLToJSONStrict(data)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid YAML: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(js, &raw); err != nil {
		return nil, nil, fmt.Errorf("invalid YAML: file must be a mapping of keys to values")
	}
	if raw == nil {
		return nil, nil, errors.New("file must be a YAML mapping; set one of context, contextRegex or server")
	}

	var warnings []string
	unknown := make([]string, 0)
	for k := range raw {
		if !contains(knownKeys, k) {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		w := fmt.Sprintf("unknown key %q", k)
		if sug := suggest(k); sug != "" {
			w += fmt.Sprintf(" (did you mean %q?)", sug)
		}
		warnings = append(warnings, w)
	}

	// encoding/json matches field names case-insensitively, so decode only the
	// exactly-spelled known keys; anything else was reported as a warning.
	known := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		if contains(knownKeys, k) {
			known[k] = v
		}
	}
	knownJSON, err := json.Marshal(known)
	if err != nil {
		return nil, warnings, err
	}
	var s Spec
	if err := json.Unmarshal(knownJSON, &s); err != nil {
		return nil, warnings, fmt.Errorf("invalid value: %w", err)
	}
	if err := s.validate(); err != nil {
		return nil, warnings, err
	}
	return &s, warnings, nil
}

func (s *Spec) validate() error {
	set := 0
	for _, v := range []string{s.Context, s.ContextRegex, s.Server} {
		if v != "" {
			set++
		}
	}
	switch {
	case set == 0:
		if s.User != "" {
			return errors.New("user can only be used together with server; set one of context, contextRegex or server")
		}
		return errors.New("no matcher; set one of context, contextRegex or server")
	case set == 2 && s.Server != "" && s.ContextRegex != "":
		// server narrowed by contextRegex: allowed.
	case set > 1:
		return errors.New("set exactly one of context, contextRegex or server (server may be combined with contextRegex or user to narrow it)")
	}
	if s.User != "" && s.Server == "" {
		return errors.New("user can only be used together with server")
	}
	if s.ContextRegex != "" {
		re, err := regexp.Compile(s.ContextRegex)
		if err != nil {
			return fmt.Errorf("contextRegex: %w", err)
		}
		s.re = re
	}
	if s.Server != "" {
		u, err := url.Parse(s.Server)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("server %q is not an absolute URL like https://host:6443", s.Server)
		}
	}
	if s.Namespace != "" && !isDNS1123Label(s.Namespace) {
		return fmt.Errorf("namespace %q is not a valid Kubernetes namespace name", s.Namespace)
	}
	return nil
}

var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func isDNS1123Label(s string) bool { return len(s) <= 63 && dns1123Label.MatchString(s) }

// isDefaultPort reports whether port is the default for scheme. It compares
// numerically, so a zero-padded port such as "0443" counts, as it does for
// kubectl and Go's dialer.
func isDefaultPort(scheme, port string) bool {
	n, err := strconv.Atoi(port)
	if err != nil {
		return false
	}
	return (scheme == "https" && n == 443) || (scheme == "http" && n == 80)
}

// NormalizeServer makes server URLs comparable: lower-case scheme and host,
// no default port (443 for https, 80 for http), no trailing slash.
func NormalizeServer(s string) string {
	s = strings.TrimSpace(s)
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return strings.TrimRight(s, "/")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	// Tools disagree on whether to spell out the default port, so
	// https://host and https://host:443 must match. Only touch hosts with a
	// real host:port split: a bracketed IPv6 address, or a host with a
	// single colon. An unbracketed IPv6 host (which kubectl rejects anyway)
	// is left alone rather than having part of its address taken as a port.
	if strings.HasPrefix(u.Host, "[") || strings.Count(u.Host, ":") == 1 {
		switch port := u.Port(); {
		case port == "":
			u.Host = strings.TrimSuffix(u.Host, ":") // e.g. "https://host:"
		case isDefaultPort(u.Scheme, port):
			// Trimming the suffix rather than rebuilding from Hostname()
			// keeps IPv6 brackets intact.
			u.Host = strings.TrimSuffix(u.Host, ":"+port)
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String()
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// suggest returns the known key closest to k, if it is plausibly a typo.
func suggest(k string) string {
	best, bestD := "", 3
	for _, c := range knownKeys {
		if d := levenshtein(strings.ToLower(k), strings.ToLower(c)); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
