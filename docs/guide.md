# kdir guide

The full reference. For a quick overview, see the [README](../README.md).

- [The `.kubectx` file](#the-kubectx-file)
- [How the isolation works](#how-the-isolation-works)
- [Fail-closed](#fail-closed)
- [Trust](#trust)
- [`protect: true`](#protect-true)
- [Commands](#commands)
- [Prompts: starship, kube-ps1](#prompts-starship-kube-ps1)
- [Scripts and CI: `kdir exec`](#scripts-and-ci-kdir-exec)
- [Compared with other tools](#compared-with-other-tools)
- [Performance](#performance)
- [Limitations](#limitations)

## The `.kubectx` file

```yaml
# How to find the context: exactly one of these
context: staging-eu                 # exact context name
# contextRegex: "^arn:aws:eks:eu-west-1:123456789012:cluster/staging$"
# server: https://ABCD1234.gr7.eu-west-1.eks.amazonaws.com

namespace: payments                 # optional; default: the context's own namespace
protect: false                      # optional; confirm mutating kubectl commands
```

| Key | Meaning |
|---|---|
| `context` | Exact context name. Simplest, but teammates often name contexts differently. |
| `contextRegex` | Go regular expression matched against context names (unanchored; add `^…$` yourself). Handles EKS ARNs and GKE-style `gke_project_zone_cluster` names. |
| `server` | The cluster's API server URL. The most robust choice for a team, because the URL is the same on everyone's machine. Compared case-insensitively, ignoring a trailing `/`. |
| `user` | Only with `server`: also require this kubeconfig user. Use it when you have several contexts for one cluster (for example admin and read-only). |
| `namespace` | Namespace to use. If omitted, the context's own namespace is used. |
| `protect` | `true` asks before mutating kubectl commands. See [`protect: true`](#protect-true). |

Rules:

- Exactly one of `context`, `contextRegex` and `server` must be set. The
  exception is `server`, which may be narrowed with `contextRegex` or `user`.
- If a matcher matches **no** context, kdir fails closed. If it matches **more
  than one**, kdir fails closed too and lists the candidates; it never picks one.
- Unknown keys are reported as a warning with a suggestion
  (`unknown key "namepsace" (did you mean "namespace"?)`), not an error.
  Keys are case-sensitive and duplicate keys are an error.

**Which file applies:** kdir walks up from the current directory and uses the
nearest `.kubectx`. Inside your home directory the walk stops at `$HOME`, so
`~/.kubectx` works as a user-wide default. Outside `$HOME` it continues to `/`.

## How the isolation works

When the shell enters a governed directory, kdir writes a tiny **overlay**
kubeconfig for that one shell and puts it at the front of `KUBECONFIG`:

```text
KUBECONFIG = $XDG_RUNTIME_DIR/kdir/<shell pid>.yaml : <your original KUBECONFIG, or ~/.kube/config>
             └──────────── overlay ─────────────┘   └──────────── your files, never written ───┘

 overlay (this shell only, mode 0600)          ~/.kube/config
 ─────────────────────────────────────         ──────────────────────────────
 current-context: staging-eu   ◀── wins        current-context: prod-eu
 contexts:                                     contexts:
   staging-eu: cluster/user refs,                staging-eu: ...
               namespace: payments ◀── wins      prod-eu:    ...
   prod-eu:    cluster/user refs               clusters:  ◀── used from here
   ...                                         users:     ◀── used from here
 (no clusters, no users, no credentials)
```

kubectl merges `KUBECONFIG` files with first-one-wins rules, so the overlay
decides the current context and namespace, while servers and credentials keep
coming from your own files. The overlay contains only context names, cluster
and user *names*, and namespaces. A test asserts it never contains a cluster,
a user or a credential.

Some details:

- **Every context is copied into the overlay** (as references), not just the
  target. kubectl writes a changed context back to the file it was loaded
  from. So `kubectl config use-context other` followed by `kubens foo` changes
  the overlay rather than your real kubeconfig. `use-context`, `kubens` and
  `kubectx` inside a governed shell therefore stay in that shell. Tests check
  this against the real kubectl.
- **Leaving** restores `KUBECONFIG` exactly (including "was unset"), unsets all
  `KDIR_*` variables and deletes the overlay.
- **Each shell is separate.** Overlays are named by shell PID. A nested shell,
  or `kdir exec`, gets its own overlay and never touches its parent's.
- **Your kubeconfig is never written by kdir.** A test runs the full
  enter/leave cycle and checks your files' bytes and mtimes are unchanged.
- **Credentials written by other tools are kept.** Tools that add clusters or
  users (`aws eks update-kubeconfig`, `gcloud container clusters get-credentials`,
  `kind create cluster`, `kubectl config set-credentials`) write to the first
  file in `KUBECONFIG`, which is the overlay. Rather than delete them when you
  leave, kdir saves the overlay as `kept-<pid>-<time>.yaml` and prints a
  one-line command to merge it into your kubeconfig. To avoid this, run those
  tools outside a governed directory.
- **If you `export KUBECONFIG=...` yourself** inside a governed directory, kdir
  treats that as your new original and puts the overlay in front of it.
- **If the overlay is deleted** (for example by a temp-file cleaner), the next
  prompt recreates it. Without this, kubectl would silently skip the missing
  file and use your normal context.

Overlays live in `$XDG_RUNTIME_DIR/kdir/`, or `$TMPDIR/kdir-<uid>/` when that
isn't set. kdir refuses to use the directory if it is a symlink or owned by
someone else, and resets its permissions to 0700. `kdir gc` deletes overlays
of shells that have exited, and kdir runs it on its own at most once a day.

## Fail-closed

If the `.kubectx` asks for a context your kubeconfig doesn't have, kdir
**does not** leave kubectl pointing at whatever was active:

```text
kdir: .kubectx wants context "staging-eu" but it isn't in your kubeconfig. kubectl is disabled in this directory until it is.
```

The overlay then selects a context that doesn't exist (`kdir-missing-staging-eu`),
so every kubectl command fails. So does every other tool that reads
`KUBECONFIG`, such as helm, k9s and terraform. The same happens when the file
doesn't parse, when a regex or server matches several contexts, or when your
kubeconfig can't be loaded. Once the context appears in your kubeconfig, the
next prompt picks it up.

An explicit `kubectl --context other ...` still works. kdir changes the
default, not what you ask for by name.

## Trust

A cloned repository must not be able to silently point your shell at a
cluster, so kdir ignores a `.kubectx` until you allow it, like `direnv allow`:

```text
kdir: .kubectx in ~/src/payments is not allowed. Run 'kdir allow' to trust it.
```

- `kdir allow [path]` records `sha256(path + contents)` in
  `$XDG_CONFIG_HOME/kdir/allowed/` (default `~/.config/kdir/allowed/`).
- If the file is **edited or moved**, it is no longer allowed. kdir switches
  back to your normal context and asks you to allow it again.
- `kdir deny [path]` removes the entry.
- `kdir init` allows the file it creates.

The notice appears once each time you enter the directory, not on every prompt.

## `protect: true`

```yaml
context: prod-eu
protect: true
```

While you're in this directory, the hook defines a `kubectl` shell function
that asks before mutating commands:

```text
$ kubectl -n web delete pod web-0
⚠ prod-eu/web: kubectl -n web delete pod web-0. Continue? [y/N]
```

- **Confirmed verbs:** `apply create delete edit patch replace scale rollout
  drain cordon uncordon taint label annotate set auth`. Their read-only
  subcommands (`rollout status`, `rollout history`, `apply view-last-applied`,
  `auth can-i`, `auth whoami`) don't ask, so only `auth reconcile` does.
- **What skips the question:** `--dry-run`, `--dry-run=client` and
  `--dry-run=server` run without asking (`--dry-run=none` really runs, so it
  asks), and so does anything run with `KDIR_YES=1`.
- **What the question shows:** the context and namespace the command will
  actually hit, including `--context`, `-n` and `-A` (shown as `*`).
- **Where it asks:** on the terminal, so you see it even when output is piped.
  With no terminal the answer is no.
- **Tab completion still works.** Completion stays bound to `kubectl`, and
  completion requests go straight to the real binary.
- **Leaving the directory removes the function.** If you already have a
  `kubectl` alias or function (say `alias kubectl=kubecolor`), kdir leaves it
  alone and tells you that protect can't apply.

> **This is a guard rail, not security.** It catches slips at an interactive
> prompt. Scripts, other tools, `command kubectl` and anything not running in
> your hooked shell bypass it. Use Kubernetes RBAC to actually
> restrict what can be done.

## Commands

| Command | What it does |
|---|---|
| `kdir init [--context X \| --regex R \| --server] [--namespace N] [--force]` | Write `.kubectx` in the current directory and allow it. With no flags it records the current context and namespace. `--server` records the current context's API server URL. Refuses to overwrite without `--force`. |
| `kdir status` | Show the governing file, whether it's allowed, the matcher, the resolved context and namespace, protect, and this shell's overlay. Exits 0 if active, 1 if no `.kubectx` applies, 2 if fail-closed, 3 if not allowed. |
| `kdir status --short` | `staging-eu:payments` for prompts, or nothing. Reads only this shell's state, never your kubeconfig. |
| `kdir allow [path]` / `kdir deny [path]` | Trust or stop trusting a `.kubectx` (default: the one governing this directory). |
| `kdir hook <zsh\|bash\|fish>` | Print the shell hook. |
| `kdir export <shell>` | Print shell code for the current directory. The hook runs this on every prompt. |
| `kdir exec -- <cmd> [args]` | Run one command with this directory's context, with no hook. See below. |
| `kdir gc` | Delete overlays of exited shells. |
| `kdir version` | Version, commit and build date. |

The hook exports these variables for prompts and scripts: `KDIR_ROOT` (the
directory containing the `.kubectx`), `KDIR_CONTEXT`, `KDIR_NAMESPACE` and
`KDIR_PROTECT`. kdir also uses `KDIR_ORIG_KUBECONFIG`,
`KDIR_ORIG_KUBECONFIG_SET`, `KDIR_OVERLAY` and `KDIR_STATE` for its own
bookkeeping. While kdir is failing closed, `KDIR_ROOT` is set but
`KDIR_CONTEXT` is not.

Errors in the hook never break your prompt: kdir prints one line to stderr
and leaves the environment as it was. The hook keeps `$?` intact for prompts
that show the last exit status.

## Prompts: starship, kube-ps1

Prompt tools read the current context from kubeconfig, and kdir changes the
kubeconfig your shell sees, so they show the right thing without any
configuration:

- **[starship](https://starship.rs/config/#kubernetes)**: the `kubernetes`
  module reads every file in `KUBECONFIG` and takes the namespace from the
  first file that defines the context. That is the overlay, which always
  carries the namespace. The module is disabled by default; enable it with
  `[kubernetes] disabled = false`.
- **[kube-ps1](https://github.com/jonmosco/kube-ps1)**: asks kubectl, and
  refreshes when `KUBECONFIG` changes or any file in it changes. Entering or
  leaving a directory changes `KUBECONFIG`, and an in-shell
  `kubectl config use-context` changes the overlay, so both show up on the
  next prompt.
- **Anything else**: use `$(kdir status --short)`, or `$KDIR_CONTEXT` and
  `$KDIR_NAMESPACE`.

The starship and kube-ps1 notes come from reading their documentation and
source, not from running them in kdir's test suite.

## Scripts and CI: `kdir exec`

The hook only runs in interactive shells. In scripts, Makefiles and CI, use
`kdir exec`:

```bash
kdir exec -- kubectl get pods
kdir exec -- helm upgrade --install payments ./chart
```

It gives the command the same environment a hooked shell would have in the
current directory, passes the command's exit status through, forwards
signals, and deletes its overlay afterwards.

Unlike the hook, **`kdir exec` refuses to run when the `.kubectx` is not
allowed**. In a script, silently using whatever context is active would be
exactly the mistake kdir exists to prevent. In CI, allow the file first:

```bash
kdir allow && kdir exec -- kubectl apply -k deploy/
```

## Compared with other tools

|  | kdir | direnv + `KUBECONFIG` | kubie | kubesess | kubech |
|---|:-:|:-:|:-:|:-:|:-:|
| Switches automatically on `cd` | ✓ | ✓ | – | – | – |
| Per-shell (other terminals unaffected) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Committable config with no credentials | ✓ | – ¹ | – | – | – |
| Finds the context by name, regex or API server URL | ✓ | – | – | – | – |
| kubectl fails if the expected context is missing | ✓ | – ² | – | – | – |
| Trust prompt for files from cloned repos | ✓ | ✓ | – | – | – |
| Optional confirmation for mutating commands | ✓ | – | – | – | – |
| Works in your current shell (no subshell) | ✓ | ✓ | – ³ | ✓ | ✓ |

¹ Either a kubeconfig with credentials lives in the repo, or each person
writes their own `.envrc`.
² Depends on what the `.envrc` does.
³ kubie starts a new shell for each context.

This is based on each project's documentation at the time of writing;
corrections are welcome. kubie, kubesess and kubech are good tools for
switching by hand; kdir is for when the directory should decide.

## Performance

The hook runs on every prompt, so the no-change path does almost nothing:
kdir compares the `.kubectx`, its trust entry and your kubeconfig files
against a fingerprint in `KDIR_STATE`, using only `stat` calls. It doesn't
parse YAML, doesn't load kubeconfig, and never makes a network call. kdir
never talks to a cluster.

Measured with [hyperfine](https://github.com/sharkdp/hyperfine) on a
50-context kubeconfig, with the `.kubectx` six directories up and the `server`
matcher (which checks every context), on an Apple-silicon Mac:

| Path | Mean ± σ | Target |
|:---|---:|---:|
| Nothing changed (every prompt in a governed directory) | 5.8 ± 0.4 ms | < 10 ms |
| Not in a governed directory | 5.6 ± 0.4 ms | |
| Switch: enter a project | 7.9 ± 0.4 ms | < 30 ms |
| Switch: leave a project | 5.5 ± 0.4 ms | |
| `kdir status --short` | 6.0 ± 0.4 ms | |
| Baseline: `kdir version` (process start-up only) | 5.4 ± 0.4 ms | |

Nearly all of it is starting the process. Reproduce with `scripts/bench.sh`;
see [benchmarks.md](benchmarks.md) for the raw results.

## Limitations

- **macOS and Linux only.** Windows is out of scope.
- Shells: zsh, bash (3.2 and later, so macOS's built-in bash works) and fish.
- The overlay copies each context's cluster, user and namespace. Context
  `extensions` (rarely used; minikube stores some metadata there) are not
  copied, so they are hidden while you're in a governed directory.
- Changes made in the shell with `kubectl config use-context` or `kubens` last
  until you leave the directory, or until the `.kubectx`, its trust or your
  kubeconfig changes. Any of those makes kdir write a fresh overlay.
- Detecting edits relies on file modification times. An edit that keeps the
  same size and timestamp can go unnoticed until the next change.
