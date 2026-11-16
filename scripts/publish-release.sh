#!/usr/bin/env bash
# Publish only the candidate already qualified by both CI lanes.
set -euo pipefail
cd "$(dirname "$0")/.."
release_tag=${RELEASE_TAG:?RELEASE_TAG is required}
release_revision=${RELEASE_REVISION:?RELEASE_REVISION is required}
repository=${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}
version=$(cat VERSION)
test "$repository" = madhifallah/guarded-batch-operator
test "$release_tag" = "v$version"
test "$(git rev-parse HEAD)" = "$release_revision"
git fetch --no-tags origin main
git merge-base --is-ancestor "$release_revision" FETCH_HEAD
git ls-remote --exit-code --tags origin "refs/tags/$release_tag" | awk '{print $1}' | grep -Fx "$release_revision"
if gh release view "$release_tag" --repo "$repository" >/dev/null 2>&1; then
  echo 'Release already exists; refusing to overwrite it.' >&2
  exit 1
fi
(cd dist/release && sha256sum --check SHA256SUMS)
python3 - "$release_tag" "$release_revision" <<'PY'
import json, sys
from pathlib import Path
release = json.loads(Path('dist/release/release.json').read_text())
assert release['tag'] == sys.argv[1]
assert release['revision'] == sys.argv[2]
assert release['repository'] == 'madhifallah/guarded-batch-operator'
assert release['platform'] == 'linux/amd64'
assert release['image'] == 'ghcr.io/' + release['repository'] + ':' + release['version']
PY
image="ghcr.io/$repository:$version"
archive="dist/release/guarded-batch-operator_${version}_image_linux_amd64.tar.gz"
docker load --input "$archive"
expected_id=$(python3 -c 'import json; print(json.load(open("dist/release/release.json"))["image_id"])')
test "$(docker image inspect --format '{{.Id}}' "$image")" = "$expected_id"
export DOCKER_CONFIG
DOCKER_CONFIG=$(mktemp -d)
trap 'python3 -c "import os, shutil; shutil.rmtree(os.environ[\"DOCKER_CONFIG\"])"' EXIT
printf '%s' "${GH_TOKEN:?GH_TOKEN is required}" | docker login ghcr.io --username "${GITHUB_ACTOR:?GITHUB_ACTOR is required}" --password-stdin
docker push "$image"
docker image inspect "$image" --format '{{json .RepoDigests}}' > dist/image-digests.json
python3 - <<'PY'
import json, re
from pathlib import Path
from importlib.util import module_from_spec, spec_from_file_location
spec = spec_from_file_location('packaging', 'scripts/package-release.py')
packaging = module_from_spec(spec)
spec.loader.exec_module(packaging)
out = Path('dist/release')
release = json.loads((out / 'release.json').read_text())
prefix = 'ghcr.io/' + release['repository'] + '@sha256:'
digests = [x for x in json.loads(Path('dist/image-digests.json').read_text()) if x.startswith(prefix)]
assert len(digests) == 1 and re.fullmatch(re.escape(prefix) + '[0-9a-f]{64}', digests[0])
release['image_digest'] = digests[0]
manifest = (out / 'install.yaml').read_text()
assert manifest.count('image: ' + release['image']) == 1
(out / 'install.yaml').write_text(manifest.replace('image: ' + release['image'], 'image: ' + digests[0]))
(out / 'release.json').write_text(json.dumps(release, indent=2) + '\n')
packaging.checksums(out)
PY
gh release create "$release_tag" dist/release/* --repo "$repository" --verify-tag --draft \
  --title "Guarded Batch Operator $release_tag" --notes-file "docs/releases/$release_tag.md"
{
  echo '### Release candidate uploaded'
  echo "Draft: https://github.com/$repository/releases"
  echo "Container: $image"
  echo 'Verify public image access and the asset checksums, then publish the draft.'
} >> "$GITHUB_STEP_SUMMARY"
