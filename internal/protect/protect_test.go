package protect

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		cmd     string
		confirm bool
		verb    string
		ctx, ns string
	}{
		{"get pods", false, "get", "", ""},
		{"apply -f x.yaml", true, "apply", "", ""},
		{"-n payments delete pod web-0", true, "delete", "", "payments"},
		{"--namespace=payments scale deploy/web --replicas=0", true, "scale", "", "payments"},
		{"--context prod-eu --v 6 edit cm/x", true, "edit", "prod-eu", ""},
		{"--context=prod-eu get pods", false, "get", "prod-eu", ""},
		{"-npayments label pod x a=b", true, "label", "", "payments"},
		{"--insecure-skip-tls-verify drain node-1", true, "drain", "", ""},
		{"apply -f x.yaml --dry-run=client", false, "apply", "", ""},
		{"apply -f x.yaml --dry-run=server", false, "apply", "", ""},
		{"apply -f x.yaml --dry-run", false, "apply", "", ""},
		{"apply -f x.yaml --dry-run=none", true, "apply", "", ""},
		{"rollout status deploy/web", false, "rollout", "", ""},
		{"rollout history deploy/web", false, "rollout", "", ""},
		{"rollout restart deploy/web", true, "rollout", "", ""},
		{"apply view-last-applied deploy/web", false, "apply", "", ""},
		{"set image deploy/web web=nginx:2", true, "set", "", ""},
		{"exec web-0 -- kubectl delete pod x", false, "exec", "", ""},
		{"config use-context dev", false, "config", "", ""},
		{"__complete delete ''", false, "__complete", "", ""},
		{"", false, "", "", ""},
		{"-n", false, "", "", ""},
		{"cordon node-1", true, "cordon", "", ""},
		{"uncordon node-1", true, "uncordon", "", ""},
		{"taint nodes n1 k=v:NoSchedule", true, "taint", "", ""},
		{"annotate pod x a=b", true, "annotate", "", ""},
		{"patch deploy web -p {}", true, "patch", "", ""},
		{"replace -f x.yaml", true, "replace", "", ""},
		{"create ns x", true, "create", "", ""},
		{"auth reconcile -f rbac.yaml", true, "auth", "", ""},
		{"auth reconcile -f rbac.yaml --dry-run=client", false, "auth", "", ""},
		{"auth can-i get pods", false, "auth", "", ""},
		{"auth whoami", false, "auth", "", ""},
		{"auth", false, "auth", "", ""},
	}
	for _, tt := range tests {
		inv := Parse(strings.Fields(tt.cmd))
		if inv.NeedsConfirmation() != tt.confirm || inv.Verb != tt.verb || inv.Context != tt.ctx || inv.Namespace != tt.ns {
			t.Errorf("kubectl %s: got %+v confirm=%v; want verb %q confirm=%v ctx %q ns %q",
				tt.cmd, inv, inv.NeedsConfirmation(), tt.verb, tt.confirm, tt.ctx, tt.ns)
		}
	}
	if !Parse([]string{"get", "pods", "-A"}).AllNS || !Parse([]string{"get", "--all-namespaces"}).AllNS {
		t.Error("all-namespaces not detected")
	}
}
