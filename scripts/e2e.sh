#!/usr/bin/env bash
set -euo pipefail
# Every kubectl invocation uses a newly created, disposable local Kind cluster.
workdir=$(mktemp -d)
cluster="guarded-batch-$$"
export KUBECONFIG="$workdir/kubeconfig"
namespace=guarded-batch-operator-system
cleanup() {
  result=$?
  if (( result != 0 )); then
    kubectl -n "$namespace" get guardedruns,jobs,pods -o wide || true
    kubectl -n "$namespace" logs deployment/guarded-batch-operator-controller-manager --tail=100 || true
  fi
  kind delete cluster --name "$cluster"
  rm -rf "$workdir"
  exit "$result"
}
trap cleanup EXIT
kind create cluster --name "$cluster" --kubeconfig "$KUBECONFIG" --wait 180s
image=${E2E_IMAGE:-guarded-batch-operator:e2e}
if [[ -z "${E2E_IMAGE:-}" ]]; then
  # The ordinary source test uses the static binary produced by make build.
  mkdir "$workdir/image"
  cp bin/manager "$workdir/image/manager"
  printf 'FROM scratch\nCOPY manager /manager\nUSER 65532:65532\nENTRYPOINT ["/manager"]\n' > "$workdir/image/Dockerfile"
  docker build -t "$image" "$workdir/image"
fi
kind load docker-image "$image" --name "$cluster"
if [[ -n "${E2E_MANIFEST:-}" ]]; then
  kubectl apply -f "$E2E_MANIFEST"
else
  cp -R config "$workdir/config"
  (cd "$workdir/config/manager" && kustomize edit set image "controller=$image")
  kustomize build "$workdir/config/default" | kubectl apply -f -
fi
kubectl wait --for=condition=Established crd/guardedruns.operations.example.org --timeout=60s
kubectl -n "$namespace" rollout status deployment/guarded-batch-operator-controller-manager --timeout=180s
runtime_identity="system:serviceaccount:$namespace:guarded-batch-operator-controller-manager"
kubectl auth can-i get configmaps --namespace="$namespace" --as="$runtime_identity"
for verb in create update patch delete; do
  if kubectl auth can-i "$verb" configmaps --namespace="$namespace" --as="$runtime_identity"; then
    echo "Runtime unexpectedly permits $verb on trusted ConfigMaps" >&2
    exit 1
  fi
done
if kubectl auth can-i get secrets --namespace="$namespace" --as="$runtime_identity"; then
  echo 'Runtime unexpectedly permits Secret reads' >&2
  exit 1
fi
kubectl -n "$namespace" create configmap maintenance-template --from-file=job.json=config/samples/job.json
kubectl -n "$namespace" create configmap maintenance-policy --from-file=policy.rego=policies/maintenance.rego
printf '{"backup":{"succeeded":true,"completedAt":"2000-01-01T00:00:00Z"},"maintenanceFreeze":false}' > "$workdir/evidence.json"
update_evidence() {
  kubectl -n "$namespace" create configmap backup-evidence --from-file="evidence.json=$workdir/evidence.json" --dry-run=client -o yaml | kubectl apply -f -
}
submit() {
  name=$1 mode=$2 template=${3:-maintenance-template} policy=${4:-maintenance-policy}
  kubectl -n "$namespace" create -f - <<YAML
apiVersion: operations.example.org/v1alpha1
kind: GuardedRun
metadata:
  name: $name
spec:
  templateRef: $template
  policyRef: $policy
  evidenceRef: backup-evidence
  mode: $mode
YAML
}
update_evidence
submit maintenance Execute
kubectl -n "$namespace" wait --for=jsonpath='{.status.phase}'=Blocked guardedrun/maintenance --timeout=60s
test "$(kubectl -n "$namespace" get jobs -o jsonpath='{.items[*].metadata.name}')" = ""
printf '{"backup":{"succeeded":true,"completedAt":"%s"},"maintenanceFreeze":false}' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$workdir/evidence.json"
update_evidence
kubectl -n "$namespace" wait --for=jsonpath='{.status.jobName}' guardedrun/maintenance --timeout=60s
job=$(kubectl -n "$namespace" get guardedrun maintenance -o jsonpath='{.status.jobName}')
job_uid=$(kubectl -n "$namespace" get job "$job" -o jsonpath='{.metadata.uid}')
kubectl -n "$namespace" rollout restart deployment/guarded-batch-operator-controller-manager
kubectl -n "$namespace" rollout status deployment/guarded-batch-operator-controller-manager --timeout=180s
kubectl -n "$namespace" wait --for=jsonpath='{.status.phase}'=Succeeded guardedrun/maintenance --timeout=120s
test "$(kubectl -n "$namespace" get job "$job" -o jsonpath='{.metadata.uid}')" = "$job_uid"
test "$(kubectl -n "$namespace" get jobs -o jsonpath='{.items[*].metadata.name}')" = "$job"
submit preview EvaluateOnly
kubectl -n "$namespace" wait --for=jsonpath='{.status.phase}'=Permitted guardedrun/preview --timeout=60s
test "$(kubectl -n "$namespace" get jobs -o jsonpath='{.items[*].metadata.name}')" = "$job"
python3 - "$workdir/failing-job.json" <<'PY'
import json,sys
with open('config/samples/job.json') as f: job=json.load(f)
job['template']['spec']['containers'][0]['command']=['sh','-c','exit 17']
with open(sys.argv[1],'w') as f: json.dump(job,f)
PY
kubectl -n "$namespace" create configmap failing-template --from-file="job.json=$workdir/failing-job.json"
submit failure Execute failing-template
kubectl -n "$namespace" wait --for=jsonpath='{.status.phase}'=Failed guardedrun/failure --timeout=120s
test "$(kubectl -n "$namespace" get guardedrun failure -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}')" = JobFailed
failed_job=$(kubectl -n "$namespace" get guardedrun failure -o jsonpath='{.status.jobName}')
kubectl -n "$namespace" wait --for=condition=Failed "job/$failed_job" --timeout=30s
printf 'E2E passed: stale evidence, re-evaluation, real execution, restart, evaluation-only and failure.\n'
