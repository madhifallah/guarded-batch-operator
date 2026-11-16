# Contributing

Open an issue describing the operational problem and a reproducible case. Keep examples generic and synthetic.

Use Go 1.27.4, Docker, kubectl, make, and Python 3. Run:

```sh
make verify test-integration security
make test-e2e
```

`make generate manifests` updates the API helpers, CRD, and runtime Role. Commit those generated files with API changes. Go tests use the standard `testing` package; Rego tests live beside their policies.

Public fork PRs do not execute on the privileged self-hosted runner pool. Maintainers review contributions and reproduce tests before moving approved code to an owned branch. No external Kubernetes cluster or cloud credentials are needed for local tests.
