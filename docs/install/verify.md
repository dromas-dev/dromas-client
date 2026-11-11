# Check a download

Each release is built from its signed tag by this repo's workflow, after its tests pass on that commit, with an SBOM and an attestation of the build, and a `checksums.txt` that the workflow signs with cosign, as GoReleaser documents. A release is immutable, and `gh release verify-asset <tag> <file>` checks a file against GitHub's attestation of it. A `go install` build is checked by Go's checksum database instead.

```sh
gh attestation verify dromas_*_linux_amd64.tar.gz -R dromas-dev/dromas-client --signer-workflow dromas-dev/dromas-client/.github/workflows/release.yml --source-ref refs/tags/<tag> --deny-self-hosted-runners   # gh 2.102.0+: built from the release's tag by this repo's workflow on a GitHub runner
gh release download -R dromas-dev/dromas-client -p checksums.txt -p checksums.txt.sigstore.json   # without gh attestation: the signed checksums, checked with cosign
cosign verify-blob --certificate-identity 'https://github.com/dromas-dev/dromas-client/.github/workflows/release.yml@refs/tags/<tag>' --certificate-oidc-issuer https://token.actions.githubusercontent.com --bundle checksums.txt.sigstore.json checksums.txt
sha256sum --ignore-missing -c checksums.txt
```
