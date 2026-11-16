# Security

This initial project has no production support guarantee. Report suspected vulnerabilities privately through the repository's GitHub security advisories.

The operator executes administrator-maintained Job templates and trusts the configured evidence producer. Granting rights to submit requests delegates that execution capability. Keep template, policy, and evidence write permissions separate from run submission. The operator watches one namespace, has no Secret-reading permission, and relies on Kubernetes admission and workload service-account permissions.

The sample policy uses synthetic backup evidence; it does not validate a real backup or restore. OPA decisions are checked before Job creation and do not revoke already-running Jobs. Job retries can repeat external side effects.

The controller's effective Roles permit ConfigMap reads and Lease-based leader election, with no ConfigMap writes or Secret reads. Kind checks these permissions using the deployed service account. Runtime builtin errors block policy evaluation, including when the failed expression appears in a negated rule. Policies must explicitly validate required evidence fields: a missing field can remain undefined without invoking a builtin. See the [16 November review](docs/review-2026-11-16.md) for fixes following the initial release and their validation scope.

CI runs only owner-authored code from this repository on ephemeral GCP Spot workers. Never add fork execution or `pull_request_target` checkout to these workflows.

The worker checks the authorized repository and run before executing workflow
code. Keep that infrastructure guard when changing runner registration or
versions; workflow conditions and runner labels alone are insufficient.
