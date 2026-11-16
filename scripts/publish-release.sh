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
# Listing includes drafts visible to this token. Any API failure must stop publication.
existing_tags=$(gh api --paginate "repos/$repository/releases?per_page=100" --jq '.[].tag_name')
if grep -Fxq -- "$release_tag" <<<"$existing_tags"; then
  echo 'Release already exists; refusing to overwrite it.' >&2
  exit 1
fi
(cd dist/release && sha256sum --check SHA256SUMS)
python3 - "$release_tag" "$release_revision" "$version" <<'PY'
import json, sys
from pathlib import Path
release = json.loads(Path('dist/release/release.json').read_text())
expected = dict(tag=sys.argv[1], revision=sys.argv[2], version=sys.argv[3],
                repository='madhifallah/guarded-batch-operator', platform='linux/amd64',
                image='ghcr.io/madhifallah/guarded-batch-operator:' + sys.argv[3])
if any(release.get(key) != value for key, value in expected.items()):
    raise ValueError('Release metadata does not match the authorized source and version')
PY
image="ghcr.io/$repository:$version"
archive="dist/release/guarded-batch-operator_${version}_image_linux_amd64.tar.gz"
docker load --input "$archive"
docker image save --output dist/loaded-image.tar "$image"
python3 scripts/release_artifacts.py dist/loaded-image.tar dist/release/release.json
rm dist/loaded-image.tar
export DOCKER_CONFIG
DOCKER_CONFIG=$(mktemp -d)
trap 'python3 -c "import os, shutil; shutil.rmtree(os.environ[\"DOCKER_CONFIG\"])"' EXIT
printf '%s' "${GH_TOKEN:?GH_TOKEN is required}" | docker login ghcr.io --username "${GITHUB_ACTOR:?GITHUB_ACTOR is required}" --password-stdin
docker push "$image"
docker image inspect "$image" --format '{{json .RepoDigests}}' > dist/image-digests.json
python3 - <<'PY'
import json, re
from pathlib import Path
import sys
sys.path.insert(0, 'scripts')
from release_artifacts import checksums
out = Path('dist/release')
release = json.loads((out / 'release.json').read_text())
prefix = 'ghcr.io/' + release['repository'] + '@sha256:'
digests = [x for x in json.loads(Path('dist/image-digests.json').read_text()) if x.startswith(prefix)]
if len(digests) != 1 or not re.fullmatch(re.escape(prefix) + '[0-9a-f]{64}', digests[0]):
    raise ValueError('Expected exactly one registry digest for the release image')
release['image_digest'] = digests[0]
manifest = (out / 'install.yaml').read_text()
if manifest.count('image: ' + release['image']) != 1:
    raise ValueError('Expected exactly one candidate image reference')
(out / 'install.yaml').write_text(manifest.replace('image: ' + release['image'], 'image: ' + digests[0]))
(out / 'release.json').write_text(json.dumps(release, indent=2) + '\n')
checksums(out)
PY
gh release create "$release_tag" dist/release/* --repo "$repository" --verify-tag --draft \
  --title "Guarded Batch Operator $release_tag" --notes-file "docs/releases/$release_tag.md"
{
  echo '### Release candidate uploaded'
  echo "Draft: https://github.com/$repository/releases"
  echo "Container: $image"
  echo 'Verify public image access and the asset checksums, then publish the draft.'
} >> "$GITHUB_STEP_SUMMARY"
