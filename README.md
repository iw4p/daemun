# daemun

대문 — *the main gate.*

A Kubernetes validating admission webhook. Zero dependencies. It mints its own
TLS certificate at startup and publishes the CA into its own webhook config, so
installing it is one `kubectl apply` — no openssl, no Secret, no cert-manager.

```bash
minikube image build -t daemun:dev .
kubectl apply -f install.yaml
```

## Policy

Lives in a ConfigMap. Edit it, apply, and a running pod picks it up within
about a minute — no rebuild, no restart.

```json
{
  "rules": [
    {
      "name": "ownership",
      "match":   { "kinds": ["Deployment"], "namespaces": ["shop"] },
      "require": { "labels": ["team", "owner"], "annotations": ["change-ticket"] }
    },
    {
      "name":   "pinned-images",
      "action": "warn",
      "forbid": { "latestImageTag": true, "labelValues": { "env": ["prod"] } }
    }
  ]
}
```

`match` narrows a rule; omitted fields mean *anything*. `action` is `deny` by
default — `warn` lets the write through and attaches the message to the kubectl
output instead. Every violation is reported at once, not one per apply.

A controller is judged on its **pod template**, so a bad Deployment is rejected
in the terminal that applied it rather than in a ReplicaSet event nobody reads.

Unknown fields are a load error. A misspelled rule fails loudly instead of
silently enforcing nothing.

## Layout

| | |
|---|---|
| `policy.go` | rules, matching, evaluation, config validation |
| `object.go` | the fields daemun reads, from any kind |
| `source.go` | loads and reloads the ConfigMap |
| `admission.go` | the AdmissionReview wire contract |
| `server.go` | the HTTP handler |
| `certs.go` `publish.go` | self-signed cert, published as the caBundle |
| `main.go` | wiring |
| `install.yaml` | everything the cluster needs |
| `policy.yaml` | the same idea as a native ValidatingAdmissionPolicy — no server at all |

```bash
go test ./...
```

## How it works

At startup the pod generates a certificate for its own Service DNS name and
PATCHes it into `validatingwebhookconfiguration/daemun` as the `caBundle`, using
a ClusterRole scoped to that one object. The API server then trusts it.

This is what Kyverno's `controllers/certmanager` does, minus rotation.

## What it cannot do

- **Mutate.** Deny or warn only. No defaulting a missing label, no sidecar
  injection.
- **Look at anything but the object in front of it.** No cross-object rules
  ("no two Ingresses may claim one host"), because that needs a cache and a
  cache can be stale.
- **Read the spec beyond images.** No resource limits, security context,
  host mounts, or probes.
- **Match on values.** Labels are present-or-absent; there is no regex or set
  membership beyond `forbid.labelValues`.
- **CronJobs.** Their pod template nests one level deeper than the others.
- **Rotate its certificate.** Good for a year, then the webhook silently stops
  being called.
- **Survive a node drain.** One replica, no PodDisruptionBudget.
- **Report anything.** Logs only — no metrics, no PolicyReport, no audit trail.

## Do you even need it?

For rules this simple, no. `policy.yaml` expresses the same thing as a native
`ValidatingAdmissionPolicy`: the API server evaluates CEL itself, so there is no
image, no Service, no certificate and nothing to keep alive.

A webhook earns its place when CEL cannot reach far enough — calling another
service, verifying an image signature, generating a second resource. That is the
line this repo sits on.

## Status

`go vet` clean, tests pass, builds. **Not yet run in a cluster.**

## Next

- Move the policy from a ConfigMap into a CRD watched with an informer: schema
  validation, `kubectl get`, per-policy RBAC. Costs `client-go`. This is the
  Kyverno shape.
- Mutating support — return a JSONPatch instead of a verdict.
- Certificate rotation.
