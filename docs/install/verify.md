# Verify a release

The release workflow of this repository builds each release from the release's signed tag, after the tests pass on that commit. With each release, the workflow publishes the following:

- A build attestation for every file that `checksums.txt` names. The attestation names the tag, the commit, and the workflow.
- A software bill of materials (SBOM) for each archive.
- `checksums.txt`, which the workflow signs with cosign, as the GoReleaser documentation describes.

Starting with v0.1.1, each release is immutable, and its tag can't move. If you build with `go install`, you don't need any of these checks, because the Go checksum database verifies the module instead.

In the commands on this page, replace `dromas_*_linux_amd64.tar.gz` with the name of the file that you downloaded, and replace `<tag>` with the tag of its release, such as `v0.6.2`.

## Verify with the GitHub CLI

You need `gh` 2.102.0 or later. The following commands check a file against the release, and check that the workflow of this repository built the file from the release's tag on a GitHub-hosted runner. Keep the `--source-ref` flag: without it, an attestation that the workflow of this repository made on any branch passes the check.

```sh
gh release verify-asset <tag> dromas_*_linux_amd64.tar.gz -R dromas-dev/dromas-client
gh attestation verify dromas_*_linux_amd64.tar.gz -R dromas-dev/dromas-client --signer-workflow dromas-dev/dromas-client/.github/workflows/release.yml --source-ref refs/tags/<tag> --deny-self-hosted-runners   # gh 2.102.0+: built from the release's tag by this repo's workflow on a GitHub runner
```

## Verify with cosign

To verify without the attestations, check the signed checksums with cosign, and then check the file against the checksums:

```sh
gh release download -R dromas-dev/dromas-client -p checksums.txt -p checksums.txt.sigstore.json
cosign verify-blob --certificate-identity 'https://github.com/dromas-dev/dromas-client/.github/workflows/release.yml@refs/tags/<tag>' --certificate-oidc-issuer https://token.actions.githubusercontent.com --bundle checksums.txt.sigstore.json checksums.txt
sha256sum --ignore-missing -c checksums.txt   # must print dromas_*_linux_amd64.tar.gz: OK
```
