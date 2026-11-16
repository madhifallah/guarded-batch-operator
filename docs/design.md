# Design

The first version contains one namespaced CRD and a Go controller built with Kubebuilder/controller-runtime. Kubernetes remains responsible for running Jobs. OPA decides whether the controller may create one.

`GuardedRun.spec` is immutable. Changing evaluation mode or requesting a retry requires another resource. The default mode is `EvaluateOnly`. Terminal results are not retried automatically.

Each execution name is derived from the Kubernetes request UID. The controller checks ownership before observing an existing Job. A successful Job creation followed by a status-write failure can be recovered by reading the same Job. An observed Job that disappears or changes UID is recorded as failed. Deleting a Job before the controller has recorded its identity remains a race; this is not an exactly-once protocol. Job retries may repeat worker side effects.

Templates contain a `batch/v1` JobSpec. The controller requires a deadline of 1–3600 seconds, a retry ceiling of 0–3, restart policy `Never`, and positive CPU/memory limits on every container. Automatic Job TTL is rejected so that a completed Job is retained until its outcome is observed. These limits do not establish an application-level resource or request budget.

The policy input is `{facts, now_unix, job}`. Time comes from the controller, not the request. The sample policy requires successful backup evidence no older than 24 hours, rejects future timestamps and maintenance freezes, and limits execution to ten minutes. The Go policy engine caches one prepared query, denies undefined decisions, requires a boolean result, and disables HTTP/DNS/runtime-inspection builtins. Compilation and evaluation have a two-second context deadline. Policy errors block execution.

Evidence freshness is meaningful only when its producer is trusted. ConfigMap contents are not proof that a backup exists or can be restored. The demo supplies synthetic facts. A production adapter must validate its source and own its evidence-write permission. The controller has no Secret-reading permission and does not copy evidence into status or Events.

The manager watches a single configured namespace. Its runtime permissions are namespaced Roles. Separate run submission rights from template/policy/evidence administration and do not treat these controls as a sandbox for hostile Job templates. Kubernetes admission and service-account permissions still apply.

Controller-runtime exposes its standard metrics on loopback port 8080; the address is configurable. Status and Events expose the operational decision. There is no public metrics service or custom webhook dependency.

Tests have distinct scopes: Go fakes exercise state transitions and failures, envtest validates the actual API/schema and controller watches, and Kind executes Pods and Jobs. envtest does not run a kubelet or Job controller.

Future milestones can add a native CronJob run submitter, trusted evidence adapters, and explicit application progress. Versioned binary/image packaging and qualification are described in [the release procedure](releasing.md).
