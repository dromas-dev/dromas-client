#!/bin/sh
# scripts/completions.sh: the shell completions of each release, written by
# cobra's completion command into share/completions/, as GoReleaser's own
# release writes its completions (its scripts/completions.sh) in its before
# hook. .goreleaser.yaml's before hook runs it; share/ is gitignored.
set -e
rm -rf share/completions
mkdir -p share/completions
for sh in bash zsh fish; do
	go run ./cmd/dromas completion "$sh" >"share/completions/dromas.$sh"
done
