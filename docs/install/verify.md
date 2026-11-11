# Verify a release

This repo's release workflow builds each release from the release's signed tag, after the tests pass on that commit. The workflow publishes, with each release:

- an attestation of the build for every file, which names the tag, the commit and the workflow
- an SBOM of each archive
- `checksums.txt`, signed with cosign by the workflow, as GoReleaser documents

From v0.1.1 on, a release is immutable, and its tag cannot move. A `go install` build needs none of this, because Go's checksum database verifies the module instead.

## Verify with the GitHub CLI

With gh 2.102.0 or later, check a file against the release, and check that this repo's workflow built the file from the release's tag on a GitHub-hosted runner. Replace `<tag>` with the release's tag, such as `v0.6.2`. Keep `--source-ref`: without it, an attestation that this repo's workflow made on any branch passes.

```sh
gh release verify-asset <tag> dromas_*_linux_amd64.tar.gz -R dromas-dev/dromas-client
gh attestation verify dromas_*_linux_amd64.tar.gz -R dromas-dev/dromas-client --signer-workflow dromas-dev/dromas-client/.github/workflows/release.yml --source-ref refs/tags/<tag> --deny-self-hosted-runners   # gh 2.102.0+: built from the release's tag by this repo's workflow on a GitHub runner
```

## Verify with cosign

Without the GitHub CLI's attestations, check the signed checksums with cosign, then check the file against the checksums:

```sh
gh release download -R dromas-dev/dromas-client -p checksums.txt -p checksums.txt.sigstore.json
cosign verify-blob --certificate-identity 'https://github.com/dromas-dev/dromas-client/.github/workflows/release.yml@refs/tags/<tag>' --certificate-oidc-issuer https://token.actions.githubusercontent.com --bundle checksums.txt.sigstore.json checksums.txt
sha256sum --ignore-missing -c checksums.txt
```
