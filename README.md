# daemun

대문 — *the main gate.*

A Kubernetes validating admission webhook in ~330 lines of Go, with **zero
dependencies**. It bootstraps its own TLS, so installing it is one
`kubectl apply` — no openssl, no Secret, no cert-manager.

The policy lives in a ConfigMap, not in the binary. Edit it, apply, and a
running pod changes behaviour with no rebuild and no restart.

```yaml
rules:
  - requireLabel: team
  - requireLabel: cost-center
    message: finance needs a cost-center label
```

## Install

```bash
minikube image build -t daemun:dev .
kubectl apply -f install.yaml
```

See [RUNBOOK.md](RUNBOOK.md) for what each step does and how to verify it.

## How it works

```
kubectl apply -f pod.yaml
        │
        ▼
  ┌─────────────┐   "may this Pod be created?"   ┌──────────────┐
  │ API server  │ ───────── HTTPS ─────────────> │ daemun pod   │
  │             │ <──────── yes / no ──────────  │              │
  └─────────────┘                                └──────────────┘
        │
        ▼
      etcd
```

At startup the pod mints a self-signed certificate in memory for its own
Service DNS name, then PATCHes that certificate into its own
`ValidatingWebhookConfiguration` as the `caBundle`. The API server now trusts
it, because it was told to by the pod — using a ClusterRole scoped to exactly
that one object.

This is what Kyverno's `controllers/certmanager` does, minus rotation.

## Layout

| file | |
|---|---|
| `main.go` | the AdmissionReview wire format, the HTTP handler, startup |
| `rules.go` | the policy: `RuleSet.Evaluate` plus a ConfigMap watcher |
| `certs.go` | generates a self-signed certificate in memory |
| `publish.go` | writes the CA into our own webhook config via the API |
| `install.yaml` | everything the cluster needs, in one file |
| `policy.yaml` | the same rule as a native `ValidatingAdmissionPolicy` — no server at all |

`RuleSet.Evaluate` knows nothing about HTTP, TLS or Kubernetes, which is what
makes it directly testable:

```bash
go test ./...
```

## Do you even need this?

Probably not, for a rule this simple. `policy.yaml` expresses the same policy as
a native `ValidatingAdmissionPolicy` — the API server evaluates CEL itself, so
there is no image, no Service, no certificate and nothing to keep alive.

A webhook earns its place when CEL cannot reach far enough: calling another
service, looking up a different object, verifying an image signature, or
generating a second resource. That is the line this repo exists to sit on.

## Status

`go vet` clean, tests pass, builds. Verified locally end to end over TLS against
a hand-written AdmissionReview.

**Not yet run in a cluster** — written while the Docker daemon was down. Expect
to fix something on the first `kubectl apply`.

## Next

- Move the rules from a ConfigMap into a CRD (`kind: LabelPolicy`) watched with
  an informer. Schema validation, `kubectl get labelpolicies`, per-policy RBAC.
  Costs `client-go` as a real dependency. This is the Kyverno shape.
- Return a JSONPatch instead of a boolean → a mutating webhook, which is how
  Istio injects sidecars.
- Rotate the certificate. It is currently good for a year, after which the
  webhook silently stops being called.
- Mutual TLS so the webhook authenticates the API server back. Kyverno issue
  #16559 is open on exactly this.

## Two things worth breaking on purpose

**Set `failurePolicy: Fail` and scale the deployment to zero.** Nothing can be
created anywhere in the cluster. That is the classic production outage, and
`kubectl delete validatingwebhookconfiguration daemun` recovers it.

**Change `resources: ["pods"]` to `["*"]`.** Every write in the cluster now
flows through this pod. Avoiding that is what Kyverno's 1,782-line
`controllers/webhook` is for — it rewrites its own registration so the API
server only calls it for resources a policy actually mentions.
