package spec

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		want     Spec
		warnings []string
		describe string
	}{
		{
			name:     "context",
			in:       "context: staging-eu\nnamespace: payments\n",
			want:     Spec{Context: "staging-eu", Namespace: "payments"},
			describe: `context "staging-eu"`,
		},
		{
			name:     "regex with protect",
			in:       "contextRegex: \"^arn:aws:eks:eu-west-1:123456789012:cluster/staging$\"\nprotect: true\n",
			want:     Spec{ContextRegex: "^arn:aws:eks:eu-west-1:123456789012:cluster/staging$", Protect: true},
			describe: `a context matching /^arn:aws:eks:eu-west-1:123456789012:cluster/staging$/`,
		},
		{
			name:     "server",
			in:       "server: https://ABCD1234.gr7.eu-west-1.eks.amazonaws.com\n",
			want:     Spec{Server: "https://ABCD1234.gr7.eu-west-1.eks.amazonaws.com"},
			describe: `server "https://ABCD1234.gr7.eu-west-1.eks.amazonaws.com"`,
		},
		{
			name:     "server narrowed by user",
			in:       "server: https://x:6443\nuser: admin\n",
			want:     Spec{Server: "https://x:6443", User: "admin"},
			describe: `server "https://x:6443" with user "admin"`,
		},
		{
			name:     "server narrowed by regex",
			in:       "server: https://x:6443\ncontextRegex: admin$\n",
			want:     Spec{Server: "https://x:6443", ContextRegex: "admin$"},
			describe: `server "https://x:6443" with context matching /admin$/`,
		},
		{
			name:     "comments and explicit false",
			in:       "# team cluster\ncontext: dev # local\nprotect: false\n",
			want:     Spec{Context: "dev"},
			describe: `context "dev"`,
		},
		{
			name:     "unknown keys warn with suggestion",
			in:       "context: dev\nnamepsace: payments\nfoo: bar\n",
			want:     Spec{Context: "dev"},
			warnings: []string{`unknown key "foo"`, `unknown key "namepsace" (did you mean "namespace"?)`},
			describe: `context "dev"`,
		},
		{
			name:     "wrong case key suggests",
			in:       "context: dev\ncontextregex: x\n",
			want:     Spec{Context: "dev"},
			warnings: []string{`unknown key "contextregex" (did you mean "contextRegex"?)`},
			describe: `context "dev"`,
		},
		{
			name:     "json syntax is yaml too",
			in:       `{"context": "dev"}`,
			want:     Spec{Context: "dev"},
			describe: `context "dev"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, warnings, err := Parse([]byte(tt.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got.re = nil
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("got %+v, want %+v", *got, tt.want)
			}
			if !reflect.DeepEqual(warnings, tt.warnings) {
				t.Errorf("warnings: got %q, want %q", warnings, tt.warnings)
			}
			if d := got.Describe(); d != tt.describe {
				t.Errorf("Describe: got %s, want %s", d, tt.describe)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{"empty", "", "file is empty"},
		{"whitespace", "  \n\n", "file is empty"},
		{"only comments", "# nothing\n", "must be a YAML mapping"},
		{"no matcher", "namespace: payments\n", "no matcher"},
		{"context and regex", "context: a\ncontextRegex: b\n", "set exactly one"},
		{"context and server", "context: a\nserver: https://x\n", "set exactly one"},
		{"all three", "context: a\ncontextRegex: b\nserver: https://x\n", "set exactly one"},
		{"user without server", "context: a\nuser: u\n", "user can only be used together with server"},
		{"user alone", "user: u\n", "user can only be used together with server"},
		{"bad regex", "contextRegex: \"(\"\n", "contextRegex"},
		{"relative server", "server: example.com\n", "not an absolute URL"},
		{"bad namespace", "context: a\nnamespace: Payments_NS\n", "not a valid Kubernetes namespace"},
		{"long namespace", "context: a\nnamespace: " + strings.Repeat("a", 64) + "\n", "not a valid Kubernetes namespace"},
		{"protect not bool", "context: a\nprotect: maybe\n", "invalid value"},
		{"context not string", "context: [a, b]\n", "invalid value"},
		{"list document", "- context: a\n", "invalid YAML"},
		{"broken yaml", "context: [a\n", "invalid YAML"},
		{"duplicate key", "context: a\ncontext: b\n", "invalid YAML"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Parse([]byte(tt.in))
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestRegexpCompiled(t *testing.T) {
	s, _, err := Parse([]byte("contextRegex: ^stag\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Regexp() == nil || !s.Regexp().MatchString("staging") {
		t.Fatal("regex not compiled")
	}
}

func TestNormalizeServer(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://ABCD.example.com", "https://abcd.example.com"},
		{"https://abcd.example.com/", "https://abcd.example.com"},
		{"HTTPS://Host:6443/", "https://host:6443"},
		{"https://host/prefix/", "https://host/prefix"},
		{"  https://host  ", "https://host"},
		// Default ports are dropped so explicit and implicit forms match.
		{"https://host:443", "https://host"},
		{"HTTPS://Host:443/", "https://host"},
		{"http://host:80", "http://host"},
		{"https://host:", "https://host"},
		{"https://10.0.0.1:443", "https://10.0.0.1"},
		{"https://[fd00::1]:443", "https://[fd00::1]"},
		{"https://[FD00::1]:6443", "https://[fd00::1]:6443"},
		// A port is only default for its own scheme.
		{"http://host:443", "http://host:443"},
		{"https://host:80", "https://host:80"},
		// Ports compare numerically, as kubectl and Go's dialer do.
		{"https://host:0443", "https://host"},
		{"http://host:080", "http://host"},
		{"https://[fd00::1]:00443", "https://[fd00::1]"},
		{"https://host:4430", "https://host:4430"},
		// An unbracketed IPv6 host has no host:port split, so it is left as is.
		{"https://fd00::", "https://fd00::"},
		{"https://fd00::443", "https://fd00::443"},
		{"https://[fd00::1]:", "https://[fd00::1]"},
		{"not a url/", "not a url"},
	}
	for _, tt := range tests {
		if got := NormalizeServer(tt.in); got != tt.want {
			t.Errorf("NormalizeServer(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
