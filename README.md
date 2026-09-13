# admission-lab

A validating admission webhook in ~130 lines of Go, with **zero dependencies**.

The policy: *every Pod must carry a `team` label.* That is deliberately trivial —
the point is not the rule, it is everything around it.

Two ways to run it:

- **dev loop** — the webhook runs on your Mac, the API server dials back out.
  Edit, `ctrl-C`, rerun. No image builds.
- **in-cluster** — the webhook runs as a Deployment behind a Service, the way a
  real one does.

---

## Dev loop

```bash
make cluster     # kind cluster (once)
make certs       # CA + server cert for host.docker.internal
make run         # terminal 1 — leave running
make register    # terminal 2
make check       # one bad Pod, one good Pod
```

---

## In-cluster (minikube)

The webhook stops being a process on your laptop and becomes a workload the
cluster is responsible for. Four things change, and all four are the reason
this step is worth doing.

```bash
minikube start                 # needs the Docker daemon running
make certs-incluster           # cert for admission-lab.admission-lab.svc
make image                     # builds into minikube's own daemon — no registry
make deploy                    # namespace, TLS secret, Deployment, Service, PDB
make register-incluster        # register against the Service
make status
make check
make logs                      # follow both replicas
```

### What actually changed

**1. `clientConfig.url` → `clientConfig.service`.** The API server no longer
dials a hostname on your network; it resolves `admission-lab.admission-lab.svc`
through cluster DNS and load-balances across the replicas.

**2. The certificate covers a different name.** `make certs-incluster` puts the
Service DNS name in the SAN instead of `host.docker.internal`. Same CA
mechanics, different name — and getting it wrong fails silently while
`failurePolicy: Ignore` is set.

**3. The cert arrives as a Secret.** `kubectl create secret tls` → mounted
read-only at `/etc/webhook/certs`, and `TLS_CERT`/`TLS_KEY` point at it. In a
real deployment something has to *rotate* that Secret before it expires. That
is the whole job of Kyverno's `controllers/certmanager`.

**4. Availability is now your problem.** Two replicas, pod anti-affinity, a
PodDisruptionBudget, and a readiness probe. With one replica, draining a node
takes the webhook down — and with `failurePolicy: Fail` that takes the cluster
with it.

### The self-deadlock guard

`deploy/in-cluster/webhook.yaml.tpl` excludes its own namespace by label:

```yaml
- key: admission-lab/self
  operator: DoesNotExist
```

Without it, the webhook has to admit its own Pods. Lose both replicas and
nothing can ever schedule them again, because the thing that would approve them
is the thing that is down. Every production webhook has some version of this
exclusion.

---

## The two experiments

**1. Watch `failurePolicy` cause an outage.** Set `failurePolicy: Fail`,
re-register, then `kubectl -n admission-lab scale deploy/admission-lab --replicas=0`.
Now try to create any Pod anywhere. Recover with `make unregister`.

**2. Widen the blast radius.** Change `resources: ["pods"]` to `["*"]` and add
`UPDATE`. Re-register and watch `make logs` while the cluster does ordinary
work — every write now flows through your webhook. Avoiding exactly this is
what Kyverno's 1,782-line `controllers/webhook` does: it rewrites its own
registration so the API server only calls it for resources a policy mentions.

---

## Status

Verified locally: `go vet` clean, builds, certs chain and carry the right SANs,
and the handler returns correct `allowed: true/false` for both fixtures.

The in-cluster manifests are written but **not yet applied** — `kubectl` needs a
running API server even to validate them, and the Docker daemon was down when
they were authored. First `make deploy` is the real test.

---

## Where to go next

- Return a JSONPatch instead of a boolean → a *mutating* webhook. That is how
  Istio injects sidecars.
- Enforce something needing a second object ("no two Pods claim the same
  `team`+`role`"). You will need a lookup, the lookup needs a cache, the cache
  can be stale. That race is inherent to admission control — no policy engine
  solves it.
- Add mutual TLS so the webhook authenticates the API server back. Kyverno
  issue #16559 is still open on exactly this.

## Cleanup

```bash
make undeploy     # in-cluster
make unregister   # dev
```
