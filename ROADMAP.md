# Roadmap

What the Dromas project plans to do over the next year, and what it doesn't plan to do. It covers the protocol, the clients in this repository, and [dromas-server](https://github.com/dromas-dev/dromas-server). The open issues in both repositories are the detailed list of work. This page is the direction, and a plan here can change.

## Planned

- **A stable v1.** The protocol is fixed at v1, and the code converges on it. v1 has no date.
- **Post-quantum signatures.** An identity can be OpenSSH's hybrid type, `ssh-mldsa44-ed25519` (ML-DSA-44 with Ed25519), as an opt-in type. Ed25519 stays the default. This work waits on the Go project's cryptography libraries supporting the type, so it's expected after v1 ([#565](https://github.com/dromas-dev/dromas-client/issues/565)).
- **Identities in hardware:** creating an identity on the Mac's Secure Enclave or a FIDO security key in the Go client, and running a mailbox on it. The Go client already verifies these key types.
- **New commands in the Go client:** `ack`, `export`, `watch`, and `recv --spool`, and an MCP server (`dromas mcp`) with a `SKILL.md` for agents.
- **More ways to install:** an Arch User Repository package, the Windows Package Manager (`winget`), and a one-line installer for Linux (`curl -fsSL https://dromas.dev/install.sh | sh`).

## Later

These are directions, not commitments for the next year.

- **A reply key:** forward secrecy for each reply, as an optional addition that clients can adopt.
- **Key transparency for revocations,** so that a server can't withhold a mailbox's revocation or show different readers different states.

## Not planned

- **Mobile push notifications.** They need an intermediate service between the server and the phone. A client that holds a connection to its server is told of a new message as it arrives.
- **Sealed sender.** Instead, a server keeps no record that joins a sender to a recipient once a message is stored.
- **Pure post-quantum signature types.** Post-quantum identities stay hybrid.
