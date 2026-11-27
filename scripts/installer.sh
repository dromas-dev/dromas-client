#!/bin/sh
# installer.sh: installs dromas on Linux, macOS and the BSDs. dromas.dev
# serves it at https://dromas.dev/install.sh:
#
#   curl -fsSL https://dromas.dev/install.sh | sh
#
# - Linux: adds the dromas repository at packages.dromas.dev to the system's
#   package manager (apt, dnf, zypper or apk) and installs the dromas package,
#   with the steps of docs/install/, so the package manager upgrades it.
# - macOS: brew install dromas-dev/tap/dromas.
# - FreeBSD, OpenBSD, NetBSD and DragonFly BSD: the latest release's archive
#   from GitHub Releases, checked against that release's checksums.txt, into
#   /usr/local/bin.
#
# Any other system, or a Linux distribution not named here, exits with
# nothing changed. A release older than those that docs/install/ lists gets a
# warning, and the install goes on.
#
# DRY_RUN=1 prints each command that changes the system instead of running it.
#
# All the work is in main, called on the last line, so a download cut short
# runs nothing.
set -eu

packages=https://packages.dromas.dev
releases=https://github.com/dromas-dev/dromas-client/releases/latest/download
docs=https://dromas.dev/docs

say() {
	printf '%s\n' "$*"
}

fail() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

# run runs a command that changes the system, through sudo or doas when this
# is not root (as_root).
run() {
	if [ "${DRY_RUN:-}" = 1 ]; then
		say "+ ${sudo:+$sudo }$*"
		return
	fi
	${sudo:+"$sudo"} "$@"
}

# as_root sets how run becomes root: as root already, then sudo, then doas.
as_root() {
	if [ "$(id -u)" = 0 ]; then
		sudo=
	elif command -v sudo > /dev/null 2>&1; then
		sudo=sudo
	elif command -v doas > /dev/null 2>&1; then
		sudo=doas
	else
		fail "installing needs root, and neither sudo nor doas is here: run this again as root, or set up sudo or doas"
	fi
}

# download URL FILE fetches URL into FILE, as this user.
download() {
	if command -v curl > /dev/null 2>&1; then
		curl -fsSL "$1" -o "$2"
	elif command -v wget > /dev/null 2>&1; then
		wget -qO "$2" "$1"
	elif command -v fetch > /dev/null 2>&1; then
		fetch -qo "$2" "$1"
	elif command -v ftp > /dev/null 2>&1; then
		ftp -Vo "$2" "$1"
	else
		fail "no curl, wget, fetch or ftp here to download with"
	fi
}

# sha256 FILE prints FILE's SHA-256 in hex.
sha256() {
	if command -v sha256sum > /dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum > /dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	elif command -v sha256 > /dev/null 2>&1; then
		sha256 -q "$1"
	elif command -v openssl > /dev/null 2>&1; then
		openssl dgst -sha256 -r "$1" | cut -d ' ' -f 1
	else
		fail "no sha256sum, shasum, sha256 or openssl here to check the download with"
	fi
}

# older A B says whether version A is older than version B, comparing the
# major and minor numbers. A version that is not numbers and dots, or none,
# is not older.
older() {
	case $1 in '' | *[!0-9.]*) return 1 ;; esac
	a1=${1%%.*}
	a2=${1#"$a1"}
	a2=${a2#.}
	a2=${a2%%.*}
	b1=${2%%.*}
	b2=${2#"$b1"}
	b2=${b2#.}
	b2=${b2%%.*}
	[ "$a1" -lt "$b1" ] || { [ "$a1" -eq "$b1" ] && [ "${a2:-0}" -lt "${b2:-0}" ]; }
}

linux() {
	case $(uname -m) in
	x86_64 | amd64 | aarch64 | arm64) ;;
	*) fail "packages.dromas.dev has no package for $(uname -m): see $docs/install/source/" ;;
	esac
	[ -r /etc/os-release ] || fail "no /etc/os-release here to name this Linux distribution: see $docs/install/binaries/"
	# shellcheck source=/dev/null
	id=$(. /etc/os-release && printf '%s' "${ID:-}")
	# shellcheck source=/dev/null
	version=$(. /etc/os-release && printf '%s' "${VERSION_ID:-}")
	case $id in
	debian) oldest=12 page=debian ;;
	ubuntu) oldest=22.04 page=debian ;;
	fedora) oldest=44 page=fedora ;;
	rhel | almalinux | rocky) oldest=8 page=fedora ;;
	amzn) oldest=2023 page=fedora ;;
	alpine) oldest=3.22 page=alpine ;;
	opensuse-leap) oldest=16.0 page=suse ;;
	sles) oldest=15.7 page=suse ;;
	arch) fail "Arch Linux has no dromas package: see $docs/install/arch/" ;;
	*)
		say "This script does not know this Linux distribution:" >&2
		say "  ID=$id VERSION_ID=$version" >&2
		say "  $(uname -a)" >&2
		say "To install a prebuilt binary, see $docs/install/binaries/" >&2
		say "To ask for this distribution, open an issue: https://github.com/dromas-dev/dromas-client/issues" >&2
		exit 1
		;;
	esac
	if older "$version" "$oldest"; then
		say "WARNING: $id $version is older than the releases Dromas supports ($docs/install/$page/)." >&2
		say "The install goes on in 10 seconds. Press Ctrl+C now to stop it." >&2
		sleep 10
	fi
	as_root
	case $page in
	debian)
		run env DEBIAN_FRONTEND=noninteractive apt-get update -qq
		run env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq ca-certificates curl
		run install -d -m 0755 /etc/apt/keyrings
		run curl -fsSL "$packages/dromas.asc" -o /etc/apt/keyrings/dromas.asc
		run curl -fsSL "$packages/deb/dromas.sources" -o /etc/apt/sources.list.d/dromas.sources
		run env DEBIAN_FRONTEND=noninteractive apt-get update -qq
		run env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq dromas
		;;
	fedora)
		run curl -fsSL "$packages/rpm/stable/dromas.repo" -o /etc/yum.repos.d/dromas.repo
		run dnf install -y dromas
		;;
	suse)
		run curl -fsSL "$packages/rpm/stable/dromas.repo" -o /etc/zypp/repos.d/dromas.repo
		run zypper --non-interactive --gpg-auto-import-keys install dromas
		;;
	alpine)
		run wget -qO /etc/apk/keys/dromas-packages.rsa.pub "$packages/dromas-packages.rsa.pub"
		if ! grep -qxF "$packages/apk/stable" /etc/apk/repositories; then
			run sh -c "echo $packages/apk/stable >> /etc/apk/repositories"
		fi
		run apk add dromas
		;;
	esac
	installed dromas
}

macos() {
	if [ "$(id -u)" = 0 ]; then
		fail "don't run this as root: Homebrew refuses root, so run it as your own user"
	fi
	command -v brew > /dev/null 2>&1 || fail "no brew here: see $docs/install/homebrew/"
	sudo=
	run brew install dromas-dev/tap/dromas
	installed dromas
}

# bsd OS installs the release archive for OS, one of GoReleaser's names.
bsd() {
	case $(uname -m) in
	x86_64 | amd64) ;;
	*) fail "the releases have no $1 archive for $(uname -m): see $docs/install/source/" ;;
	esac
	as_root
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	download "$releases/checksums.txt" "$tmp/checksums.txt"
	line=$(grep -E "^[0-9a-f]{64}  dromas_[0-9.]+_$1_amd64\.tar\.gz\$" "$tmp/checksums.txt") ||
		fail "the latest release's checksums.txt names no $1 archive"
	want=${line%% *}
	archive=${line##* }
	download "$releases/$archive" "$tmp/$archive"
	[ "$(sha256 "$tmp/$archive")" = "$want" ] ||
		fail "$archive does not match the release's checksums.txt: nothing installed"
	(cd "$tmp" && tar -xzf "$archive" dromas)
	run install -d -m 0755 /usr/local/bin
	run install -m 0755 "$tmp/dromas" /usr/local/bin/dromas
	installed /usr/local/bin/dromas
}

# installed DROMAS prints what was installed and the next step.
installed() {
	[ "${DRY_RUN:-}" = 1 ] && return
	say ""
	say "Dromas is installed:"
	"$1" version
	say ""
	say "Next, create a mailbox:"
	say ""
	say "  dromas init"
	say ""
	say "Then follow the quickstart: $docs/quickstart/"
}

main() {
	case $(uname -s) in
	Linux) linux ;;
	Darwin) macos ;;
	FreeBSD) bsd freebsd ;;
	OpenBSD) bsd openbsd ;;
	NetBSD) bsd netbsd ;;
	DragonFly) bsd dragonfly ;;
	MINGW* | MSYS* | CYGWIN*) fail "on Windows, see $docs/install/windows/" ;;
	*) fail "this script does not install on $(uname -a): see $docs/install/" ;;
	esac
}

main "$@"
