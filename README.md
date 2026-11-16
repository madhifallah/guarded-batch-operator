# Guarded Batch Operator

[![CI](https://github.com/madhifallah/guarded-batch-operator/actions/workflows/ci.yml/badge.svg)](https://github.com/madhifallah/guarded-batch-operator/actions/workflows/ci.yml)

A Kubernetes operator written in Go that checks operational prerequisites before launching bounded batch Jobs. OPA/Rego policies decide whether trusted evidence permits an execution.

Use it for maintenance, archive cleanup, exports, or imports that require a recent backup or an open maintenance window. The included demo uses synthetic evidence and an echo workload.

[![Architecture: a Go controller evaluates trusted ConfigMaps with OPA before creating bounded Kubernetes Jobs; blocked and evaluation-only runs create no Job.](docs/diagrams/architecture.svg)](docs/diagrams/architecture.svg)

The diagram's [editable HTML source](docs/diagrams/architecture.html) and the [design notes](docs/design.md) describe the control flow and trust boundary.

## How it works

1. An administrator maintains three ConfigMaps: a Job template, a Rego policy, and evidence supplied by a trusted producer.
2. A user creates an immutable `GuardedRun` referencing those ConfigMaps.
3. The controller evaluates `data.guarded.allow`. Missing evidence, invalid policies, and denied decisions block execution.
4. `EvaluateOnly` reports the decision without launching a Job. `Execute` creates a Job named from the request UID.
5. The controller observes the Job and records `Running`, `Succeeded`, or `Failed`, with status conditions and Events.

Blocked runs are re-evaluated when configuration changes and every 30 seconds. A controller restart finds the existing owned Job. Once its execution has been observed, a missing or replaced Job fails the request instead of silently launching another execution.

## Try it locally

Requirements for the local test suite: Linux, Go **1.27.4**, Docker, make, and Python 3. Kind, Kustomize, and checksum-verified kubectl are installed into `bin/`. Development tools are installed at pinned versions into `bin/`.

```sh
make test policy-test
make test-integration
make test-e2e
```

The end-to-end command creates its own Kind cluster and kubeconfig, exercises real Jobs, restarts the controller, and removes only its test cluster. It does not use your current Kubernetes context.

To build the controller image:

```sh
make image IMG=guarded-batch-operator:dev
```

The deployment in `config/default` uses `controller:latest` as an image placeholder. Set that image with Kustomize before deploying. `make install` and `make deploy` explicitly target your current Kubernetes context; the operator watches only its configured namespace.

Example request:

```yaml
apiVersion: operations.example.org/v1alpha1
kind: GuardedRun
metadata:
  name: maintenance-preview
spec:
  templateRef: maintenance-template
  policyRef: maintenance-policy
  evidenceRef: backup-evidence
  mode: EvaluateOnly
```

See [the sample Job](config/samples/job.json), [backup freshness policy](policies/maintenance.rego), and [design notes](docs/design.md).

## CI

GitHub Actions runs on an ephemeral GCP Spot pool. Two lanes cover Go race tests, Rego tests, API integration with envtest, vulnerability analysis, generated manifests, and real Job execution with Kind. Actions and development tools are pinned. A successful quality lane uploads the Linux binary, installation manifest, and coverage report.

The self-hosted pool runs owner-authored pushes and internal PRs only. Fork PRs and other actors do not receive these runners. Contributors can run the same checks locally. CI cancellation on a newer push is disabled.

## Releases

[Versioned releases](https://github.com/madhifallah/guarded-batch-operator/releases) target **Linux AMD64** and include a controller binary archive, a loadable Docker image archive, `install.yaml`, release metadata, and `SHA256SUMS`. The registry image is `ghcr.io/madhifallah/guarded-batch-operator:0.1.0`. Published installation manifests pin the image by digest.

Download and verify the first release before installing it into your chosen Kubernetes context:

```sh
curl -fsSLO https://github.com/madhifallah/guarded-batch-operator/releases/download/v0.1.0/install.yaml
curl -fsSLO https://github.com/madhifallah/guarded-batch-operator/releases/download/v0.1.0/SHA256SUMS
sha256sum --ignore-missing --check SHA256SUMS
kubectl apply -f install.yaml
```

To inspect the image without a cluster, run `docker run --rm ghcr.io/madhifallah/guarded-batch-operator:0.1.0 --help`. The image runs as UID/GID 65532. For offline loading, download the image archive and use `docker load --input guarded-batch-operator_0.1.0_image_linux_amd64.tar.gz`.

CI qualifies the packaged image and manifest in Kind before a version tag can produce a draft release. See [the release procedure](docs/releasing.md) and [v0.1.0 release notes](docs/releases/v0.1.0.md). Ordinary [CI artifacts](https://github.com/madhifallah/guarded-batch-operator/actions/workflows/ci.yml) remain available for seven days.

## Scope

This is an initial implementation, not evidence of production adoption. Evidence is a trusted ConfigMap contract; production backup integrations are not included. It does not schedule work, enforce global concurrency or provider spending limits, detect application progress, or promise exactly-once external side effects. Workers must tolerate retries. Run creation delegates the authority to execute administrator-provided templates; restrict template, policy, and evidence writes accordingly.

Licensed under Apache-2.0.
