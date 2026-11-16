# Working on Guarded Batch Operator

Keep this project generic. Use original code, synthetic fixtures, example.org names, and no customer configuration or datasets.

Use the standard Go test framework. Preserve immutable execution requests, ownership checks, fail-closed policy evaluation, namespace-scoped runtime permissions, and the distinction between Job creation and exactly-once worker effects.

Run `make verify test-integration security` and `make test-e2e` for controller changes. End-to-end tests must use their own Kind cluster and kubeconfig. Do not use a production Kubernetes context for tests.

CI uses the existing GCP Spot pool with per-run labels. Keep owner/internal-PR restrictions, SHA-pinned Actions, bounded job timeouts, and `cancel-in-progress: false`. Runner provisioning configuration lives outside this public repository.
