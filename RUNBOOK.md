# Run it

Two commands.

```bash
minikube start

minikube image build -t daemun:dev .
kubectl apply -f install.yaml
```

That's the install. No openssl, no secrets, no base64 — same as installing Kyverno.

Watch it come up:

```bash
kubectl -n daemun logs -l app=daemun -f
```

You should see:

```
generated a certificate for [daemun.daemun.svc daemun.daemun.svc.cluster.local]
published our CA into validatingwebhookconfiguration/daemun
listening on :8443
policy: every Pod must have a "team" label
```

Try it, in another terminal:

```bash
kubectl apply -f deploy/pod-bad.yaml    # no team label  → rejected
kubectl apply -f deploy/pod-good.yaml   # team=platform  → created
```

Remove it:

```bash
kubectl delete -f install.yaml
```

---

## Where the certificate went

You asked why this was your problem. It isn't any more — but something still has
to do it, so the pod does it to itself at startup:

| step | code | what happens |
|---|---|---|
| 1 | `certs.go` | pod generates a certificate in memory for its own Service name |
| 2 | `publish.go` | pod PATCHes that certificate into `validatingwebhookconfiguration/daemun` as `caBundle` |
| 3 | `main.go` | pod serves HTTPS with it |

Now the API server trusts the pod, because the pod told it to — and it was
allowed to, because of the ClusterRole in `install.yaml`:

```yaml
resources: ["validatingwebhookconfigurations"]
resourceNames: ["daemun"]
verbs: ["get", "patch"]
```

It can edit that one object and nothing else.

Look at what it wrote:

```bash
kubectl get validatingwebhookconfiguration daemun \
  -o jsonpath='{.webhooks[0].clientConfig.caBundle}' | head -c 60
```

That field was empty in `install.yaml`. The pod filled it in.

**This is exactly what Kyverno does.** Its `controllers/certmanager` is the same
idea with rotation added, which is the part we skipped — our certificate is good
for a year and then the webhook silently stops working.

---

## What is in install.yaml

Seven objects, in order:

| | why |
|---|---|
| `Namespace` | somewhere to live |
| `ServiceAccount` | an identity for the pod |
| `ClusterRole` + `Binding` | lets it patch its own webhook config |
| `Deployment` | the webhook itself |
| `Service` | a stable name for the API server to dial |
| `ValidatingWebhookConfiguration` | the note telling the API server when and where to call |

The last one is what "registering" means — it's just a YAML object.

---

## If something breaks

**`ImagePullBackOff`** — the image was built outside minikube. Rerun
`minikube image build -t daemun:dev .`

**Pod crash-looping with "could not publish our CA"** — RBAC didn't apply, or
the `ValidatingWebhookConfiguration` isn't there yet. Reapply `install.yaml`.

**Pods get created and the log shows nothing** — the API server couldn't reach
the pod and gave up quietly, because `failurePolicy: Ignore`. Check the pod is
`Running` and the Service has an endpoint:

```bash
kubectl -n daemun get pods,endpoints
```

**Nothing can be created anywhere** — you set `failurePolicy: Fail` and the pod
is down:

```bash
kubectl delete validatingwebhookconfiguration daemun
```
