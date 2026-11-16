# Initial validation

Local qualification on 16 November 2026 used Go 1.27.4, controller-runtime 0.25.3, OPA 1.22.4, and Kubernetes 1.37.0.

- `make verify`: formatting, vet, race-enabled Go tests, five Rego tests, and generated API/CRD/RBAC consistency passed. The policy engine had 100% statement coverage; the controller had 74.8% in the initial unit suite.
- `make test-integration`: passed against a real envtest API server. Checked default evaluation mode, immutable requests, invalid-mode rejection, real reconciliation, Job ownership, and recovery through a new reconciler.
- `make security`: govulncheck found no reachable vulnerable symbols. It also reported two module-level findings outside the imported/reachable code.
- `make test-e2e`: passed in an isolated Kind cluster using the actual static Go manager and namespace-scoped runtime Role. Checked stale evidence blocking, fresh evidence re-evaluation, real execution, controller rollout/restart with unchanged Job UID, evaluation without execution, and a failed worker. The test cluster was removed.

Two earlier local end-to-end attempts caught a test-image build context that excluded the binary and a missing startup flag. Both were corrected before the successful run. These results describe local qualification, not production adoption or completed remote CI.

Both remote quality and Kubernetes end-to-end jobs passed on ephemeral GCP Spot
workers in [the initial CI campaign](https://github.com/madhifallah/guarded-batch-operator/actions/runs/43875612839)
at source `0ccd020`. The official artifact's digest and clean binary source
revision were verified. The workflow badge reports the current remote status.

The pool admits owner-authored repository runs and checks repository identity,
run ID and attempt before workflow code executes. Scheduling labels are routing
information; they do not establish permission to execute code. Workers retain
repository affinity, including their caches. Runner provisioning and its
qualification remain outside this generic public project.
