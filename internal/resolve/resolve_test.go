package resolve

import (
	"reflect"
	"strings"
	"testing"

	"github.com/thomaschaplin/kdir/internal/kubeconfig"
	"github.com/thomaschaplin/kdir/internal/spec"
	"github.com/thomaschaplin/kdir/internal/testutil"
)

func TestResolve(t *testing.T) {
	cfg, err := kubeconfig.Load([]string{testutil.Fixture("kubeconfig/basic.yaml")})
	if err != nil {
		t.Fatal(err)
	}
	const stagingServer = "https://ABCD1234.gr7.eu-west-1.eks.amazonaws.com"
	tests := []struct {
		name string
		spec string
		want Result
	}{
		{"exact", "context: staging-eu", Result{Status: Found, Context: "staging-eu", Namespace: "default"}},
		{"exact with namespace override", "context: staging-eu\nnamespace: payments", Result{Status: Found, Context: "staging-eu", Namespace: "payments"}},
		{"exact without context namespace", "context: dev", Result{Status: Found, Context: "dev", Namespace: ""}},
		{"exact missing", "context: staging-us", Result{Status: Missing}},
		{"exact is not a prefix match", "context: staging", Result{Status: Missing}},
		{"regex unique (EKS ARN)", `contextRegex: "^arn:aws:eks:eu-west-1:123456789012:cluster/staging$"`,
			Result{Status: Found, Context: "arn:aws:eks:eu-west-1:123456789012:cluster/staging", Namespace: "kube-system"}},
		{"regex ambiguous", "contextRegex: staging", Result{Status: Ambiguous, Candidates: []string{
			"arn:aws:eks:eu-west-1:123456789012:cluster/staging", "staging-eu", "staging-eu-readonly"}}},
		{"regex missing", "contextRegex: ^gke_", Result{Status: Missing}},
		{"server unique", "server: https://PROD5678.gr7.eu-west-1.eks.amazonaws.com", Result{Status: Found, Context: "prod-eu", Namespace: "payments"}},
		{"server case and slash insensitive", "server: https://prod5678.gr7.eu-west-1.eks.amazonaws.com/", Result{Status: Found, Context: "prod-eu", Namespace: "payments"}},
		{"server explicit default port", "server: https://prod5678.gr7.eu-west-1.eks.amazonaws.com:443", Result{Status: Found, Context: "prod-eu", Namespace: "payments"}},
		{"server non-default port is distinct", "server: https://prod5678.gr7.eu-west-1.eks.amazonaws.com:6443", Result{Status: Missing}},
		{"server ambiguous", "server: " + stagingServer, Result{Status: Ambiguous, Candidates: []string{
			"arn:aws:eks:eu-west-1:123456789012:cluster/staging", "staging-eu", "staging-eu-readonly"}}},
		{"server narrowed by user", "server: " + stagingServer + "\nuser: staging-readonly", Result{Status: Found, Context: "staging-eu-readonly"}},
		{"server narrowed by user still ambiguous", "server: " + stagingServer + "\nuser: staging-admin", Result{Status: Ambiguous, Candidates: []string{
			"arn:aws:eks:eu-west-1:123456789012:cluster/staging", "staging-eu"}}},
		{"server narrowed by regex", "server: " + stagingServer + "\ncontextRegex: ^staging-eu$\nnamespace: web", Result{Status: Found, Context: "staging-eu", Namespace: "web"}},
		{"server missing", "server: https://nope.example.com", Result{Status: Missing}},
		{"server with user missing", "server: " + stagingServer + "\nuser: prod-admin", Result{Status: Missing}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, err := spec.Parse([]byte(tt.spec))
			if err != nil {
				t.Fatal(err)
			}
			got := Resolve(s, cfg)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveDanglingClusterReference(t *testing.T) {
	dir := t.TempDir()
	path := testutil.WriteFile(t, dir+"/config", `apiVersion: v1
kind: Config
contexts:
- name: orphan
  context: {cluster: gone, user: u}
`)
	cfg, err := kubeconfig.Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	s, _, _ := spec.Parse([]byte("server: https://gone.example.com"))
	if got := Resolve(s, cfg); got.Status != Missing {
		t.Fatalf("got %+v, want missing", got)
	}
}

// The kubeconfig side is normalized too: a cluster written with an explicit
// default port matches a spec written without one.
func TestResolveKubeconfigDefaultPort(t *testing.T) {
	dir := t.TempDir()
	path := testutil.WriteFile(t, dir+"/config", `apiVersion: v1
kind: Config
clusters:
- name: prod
  cluster: {server: "https://API.example.com:443/"}
- name: dev
  cluster: {server: "https://api.example.com:6443"}
contexts:
- name: prod
  context: {cluster: prod, user: u, namespace: payments}
- name: dev
  context: {cluster: dev, user: u}
users:
- name: u
  user: {}
`)
	cfg, err := kubeconfig.Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	s, _, _ := spec.Parse([]byte("server: https://api.example.com"))
	if got := Resolve(s, cfg); got.Status != Found || got.Context != "prod" || got.Namespace != "payments" {
		t.Fatalf("got %+v, want prod/payments", got)
	}
}

func TestResolveFiftyContexts(t *testing.T) {
	dir := t.TempDir()
	path := testutil.WriteFile(t, dir+"/config", testutil.GenKubeconfig(50, "ctx-0"))
	cfg, err := kubeconfig.Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	s, _, _ := spec.Parse([]byte("server: https://cluster-42.example.com:6443"))
	got := Resolve(s, cfg)
	if got.Status != Found || got.Context != "ctx-42" || got.Namespace != "ns-42" {
		t.Fatalf("got %+v", got)
	}
	s, _, _ = spec.Parse([]byte(`contextRegex: "^ctx-4\\d$"`))
	if got := Resolve(s, cfg); got.Status != Ambiguous || len(got.Candidates) != 10 {
		t.Fatalf("got %+v", got)
	}
}

func TestMessage(t *testing.T) {
	s, _, _ := spec.Parse([]byte("context: staging-eu"))
	got := Result{Status: Missing}.Message(s)
	want := `.kubectx wants context "staging-eu" but it isn't in your kubeconfig. kubectl is disabled in this directory until it is.`
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	s, _, _ = spec.Parse([]byte("contextRegex: staging"))
	got = Result{Status: Ambiguous, Candidates: []string{"a", "b"}}.Message(s)
	if !strings.Contains(got, "2 contexts match (a, b)") || strings.Contains(got, "\n") {
		t.Errorf("got %q", got)
	}
	if got := (Result{Status: Found}).Message(s); got != "" {
		t.Errorf("found result should have no message, got %q", got)
	}
}
