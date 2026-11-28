// Package protect decides whether a kubectl invocation in a `protect: true`
// directory needs confirmation. It is a guard rail against slips, not a
// security boundary: anything that runs kubectl without going through the
// shell function (scripts, aliases, other tools) is not covered.
package protect

import (
	"strings"
)

// MutatingVerbs are the kubectl commands that ask for confirmation.
var MutatingVerbs = map[string]bool{
	"apply": true, "create": true, "delete": true, "edit": true, "patch": true,
	"replace": true, "scale": true, "rollout": true, "drain": true, "cordon": true,
	"uncordon": true, "taint": true, "label": true, "annotate": true, "set": true,
	// auth reconcile creates or updates RBAC roles and bindings.
	"auth": true,
	// These create or change objects too: run (a pod), expose (a service),
	// autoscale (an HPA), certificate approve/deny (a CSR's status) and
	// debug (an ephemeral container in a running pod, or a new pod).
	"run": true, "expose": true, "autoscale": true, "certificate": true,
	"debug": true,
}

// readOnlySubcommands are subcommands of mutating verbs that only read.
var readOnlySubcommands = map[string]map[string]bool{
	"rollout": {"status": true, "history": true},
	"apply":   {"view-last-applied": true},
	// A bare "kubectl auth" only prints help.
	"auth": {"can-i": true, "whoami": true, "": true},
	// A bare "kubectl certificate" only prints help; approve and deny are
	// its only other subcommands.
	"certificate": {"": true},
}

// valueFlags are kubectl global flags that take a separate value argument
// (e.g. "-n payments"), so the value is not mistaken for the verb.
var valueFlags = map[string]bool{
	"--as": true, "--as-group": true, "--as-uid": true, "--as-user-extra": true,
	"--cache-dir": true, "--certificate-authority": true, "--client-certificate": true,
	"--client-key": true, "--cluster": true, "--context": true, "--kubeconfig": true,
	"--kuberc": true, "--log-flush-frequency": true, "-n": true, "--namespace": true,
	"--password": true, "--profile": true, "--profile-output": true, "--proxy-url": true,
	"--request-timeout": true, "-s": true, "--server": true, "--tls-server-name": true,
	"--token": true, "--user": true, "--username": true, "-v": true, "--v": true,
	"--vmodule": true,
}

// Invocation is what kdir understood of a kubectl command line.
type Invocation struct {
	Verb       string
	Subcommand string
	DryRun     bool
	Help       bool   // -h/--help: kubectl only prints usage
	Context    string // from --context, if given
	Namespace  string // from -n/--namespace, if given
	AllNS      bool   // -A/--all-namespaces
}

// Parse extracts the verb and the flags that matter for confirmation.
func Parse(args []string) Invocation {
	var inv Invocation
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break // the rest belongs to e.g. kubectl exec's command
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		name, value, hasValue := strings.Cut(a, "=")
		// -nfoo is -n foo.
		if !hasValue && len(a) > 2 && a[1] != '-' && valueFlags[a[:2]] {
			name, value, hasValue = a[:2], a[2:], true
		}
		if !hasValue && valueFlags[name] && i+1 < len(args) {
			value = args[i+1]
			i++
		}
		switch name {
		case "--context":
			inv.Context = value
		case "-n", "--namespace":
			inv.Namespace = value
		case "-A", "--all-namespaces":
			inv.AllNS = value == "" || value == "true"
		case "-h", "--help":
			inv.Help = !hasValue || value != "false"
		case "--dry-run":
			// --dry-run (deprecated bool), --dry-run=client|server are dry
			// runs; --dry-run=none and --dry-run=false really run.
			inv.DryRun = !hasValue || (value != "none" && value != "false")
		}
	}
	if len(positional) > 0 {
		inv.Verb = positional[0]
	}
	if len(positional) > 1 {
		inv.Subcommand = positional[1]
	}
	return inv
}

// NeedsConfirmation reports whether the invocation must be confirmed.
func (inv Invocation) NeedsConfirmation() bool {
	if !MutatingVerbs[inv.Verb] || inv.DryRun || inv.Help {
		return false
	}
	return !readOnlySubcommands[inv.Verb][inv.Subcommand]
}
