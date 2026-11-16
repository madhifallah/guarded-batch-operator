# Releasing

`VERSION` owns the binary/image release version; the Kubernetes API version remains independent. Releases currently support Linux AMD64. Builds, checks, packaged-image tests, and publication use the existing ephemeral Spot pool.

1. Update `VERSION`, release notes at `docs/releases/v<version>.md`, and the README download/image references in a PR. Run `make verify test-integration security test-release`. Packaging requires a clean, previously unused `dist/release` output directory; remove only that owned generated directory before a new local qualification.
2. Merge after both CI lanes pass. Create a lightweight `v<version>` tag on that main commit and push the tag. Do not move or reuse a published version tag.
3. The tag runs quality checks and packages the controller. The end-to-end lane verifies checksums, loads the actual Docker archive, and applies its installation manifest in an isolated Kind cluster. Only after both lanes pass can publication use the qualified candidate artifact.
4. The publisher checks source ancestry, tag identity, version, checksums, and image ID, then pushes the image using the job's short-lived `GITHUB_TOKEN`. It replaces the manifest's image tag with the registry digest, refreshes checksums, and uploads a **draft** GitHub Release. Existing releases are refused rather than overwritten. No long-lived package credential is required.
5. On the first publication, change the new container package's visibility to **Public** in its GitHub package settings. Repository permission inheritance does not make a new container package public. See [GitHub's visibility documentation](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility). Verify anonymous image access, the image ID/source labels, installation-manifest digest, and all downloaded asset checksums. Then publish the draft. Later versions reuse the public package.

Candidate metadata identifies its source commit, version, platform, image ID, and archive filenames. Published metadata additionally records the registry digest. `SHA256SUMS` covers every payload and metadata file and excludes itself. These checksums are not a signing attestation.

The publisher has `contents: write` and `packages: write`; other jobs retain read-only repository permissions. Forks and other actors do not receive the Spot runners. Tag publication reuses the configured quality pool route after both test lanes finish. Automatic cancellation remains disabled.

The Docker archive and registry image share an image ID. Kind qualifies the candidate manifest with its version tag; publication changes only that image reference to the digest returned by the registry. No production Kubernetes context is used during qualification or publication.
