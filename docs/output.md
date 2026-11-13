# Output format

Every `dromas` command prints [JSON Lines](https://jsonlines.org/): one JSON object per line, with `type` as the first member, naming what the line reports. Help, and the notice that `-h` is deprecated in favour of `--help`, are the only output in plain text.

A command prints what it did, and what it met on the way, to stdout, messages and refusals alike. The only JSON lines a command prints to stderr are these:

- `error`, when the command failed
- `usage_error`, when the command line was invalid
- `config_error`, when a setting was invalid, in the config file, a flag or an environment variable
- `waiting`, while the command waits

`dromas recv --log-file <file>` appends the lines it would print, on stdout and on stderr, to the file instead. The `error` line it ends with goes to the file too. A `usage_error` or `config_error` line still goes to stderr, because `recv` stops before it opens the file.

A `detail` member is the client's own words, never a sender's or a server's.

## Line types

| `type` | Printed by | Members |
|---|---|---|
| `message` | `recv`, `peer allow`, `inbox list`, `inbox show` | `id`; `alias`, this mailbox's name for the sender, when it named one (`dromas alias add`: the mailbox's word, never the sender's); then `envelope`, the envelope's checked fields: `v`, `from`, `to`, `sent_at`, `msg_id`, `body`, and `subject`, `in_reply_to`, `supersedes` when set. `inbox/<id>.json` is the envelope alone, and `sent/` keeps it as `envelope`, beside the server's `receipt`. Its strings escape every character nobody sees, Unicode's `Default_Ignorable_Code_Point`, every control character, U+FFF9 to U+FFFB, and each line break but LF: the same value, with nothing hidden in it |
| `message_headers` | `recv --headers`, `inbox list --headers`, `inbox show --headers` | in each `message` line's place, with none of the text the sender chose: `id`; `alias`, as on `message`; `envelope`, with no body or subject: `v`, `from`, `to`, `sent_at`, `msg_id`, `in_reply_to` and `supersedes` when each has a `msg_id`'s form; and `body_size`, the body's bytes. `dromas inbox show <id>` prints the `message` line |
| `refused` | `recv`, `refused list` | `id`, `code` (below), `detail`. The message is kept in `refused/`. From `refused list`, also `received_at`, the server's, when it has its form; then what the checks established when they judged it: `judged_at`, this machine's clock; `signed_by` when its signature verifies, by that identity, or by a working key it certified at that certificate's own start under a certificate the mailbox holds no revocation of, and the mailbox holds no revocation of that address, or else `claims_identity`; and with `signed_by`, `sent_at`, `msg_id` and, under a certificate, `certificate_serial`, `certificate_not_before` and `certificate_not_after` |
| `held`, `dropped`, `duplicate` | `recv` | `id`, `detail`; `held` (kept in `held/`) has `from`. `dropped` is a message from a sender this mailbox blocked |
| `rate_limited`, `more` | `recv` | `detail`: the rest is still on the server: run `recv` again, after `retry_after` seconds for `rate_limited` |
| `exec_attempt_failed`, `exec_failed`, `exec_skipped` | `recv --exec` | `id`, `detail`. `exec_attempt_failed` is one failed run of the `--exec` program for a message: `attempt`, then `exit_status`, `signal` or `start_error`, and `timed_out` where `--exec-timeout` killed the run. `exec_failed` is a message with no attempt left: `attempts`, and `status` lists it under `attention` and in `failed` until `dromas inbox clear-failed` clears every one, or its file in `inbox/` is deleted. `exec_skipped` is a message owed a run of the program that `inbox/` no longer keeps as recv wrote it. None quotes the message or the program's output, which is discarded unread |
| `renewal_failed`, `acl_sync_failed` | `recv`; `acl_sync_failed` also `send` and `outbox flush`, which post when the ACL the server last took admits the reply | the members of an `error` line |
| `acl_replaced` | `recv` | `detail`: the server held an ACL this mailbox had not sent, written by another machine of it, someone holding its pass or the server, and the mailbox's own replaced it |
| `prekey_not_made` | `recv`, `prekey renew` | `serial`, `read_at`, `detail`, under a certificate `certificate_serial` and `own_certificate`, and once its server has accepted a statement of the mailbox's own above it, `renewed_above`, that statement's serial: at the mailbox's read of itself at `read_at`, its server served a prekey statement in its name that the mailbox did not make, signed by its identity or under a certificate it did not make (its identity is in another's hands), or under one it made (`own_certificate`: its working key, and the identity beside it, were copied). Either way it recommends revoking the address; `recv` renews nothing above the statement, and `dromas prekey renew`, for an owner who knows whose it is, does, which takes the address back but not the identity, so the recommendation stands. A mailbox restored from its identity alone meets the lost machine's. `status` says so too, until a read serves the mailbox's own |
| `lookup_failed` | `cert revoke-earlier` | the members of an `error` line: the mailbox's own server could not be read, or served what the mailbox does not accept, and the remedy went on from what the mailbox knows |
| `sent` | `send`, `outbox flush`, `recv --follow` | `id`, `msg_id`, `to`, and `alias` when the mailbox named the recipient |
| `queued` | `send` to a group, `outbox flush` | `msg_id`, `to`, `alias` when the mailbox named the recipient, and `at`, when the message is due: the mailbox's budget of sends does not yet hold the group's members still to go, or a refusal about this mailbox or the path stopped the flush before it, for the server's `Retry-After` |
| `send_failed` | `recv --follow` | `code`, `detail`, and the members of an `error` line: a flush left messages in the outbox, each posted again once it is due |
| `mailbox` | `init`, `addr` | `address`, `link`; from `init`, a `detail` warning when the identity is not `ssh-ed25519` |
| `restore` | `init --restore`: before `mailbox`; or, where its acknowledgement committed and init then failed, before the `error` line | `acknowledged`, `detail`: `true` where `state` did not match `prekeys/`, now acknowledged; `false` where it matched, and nothing changed |
| `deprecated`, `sunset` | any command that called a server, last | `version`, this client's; `deprecated_at` or `sunset_at`, the date from which the server deems it deprecated, or stops accepting it, from the answers' `Deprecation` or `Sunset` (`PROTOCOL.md` §11.17); `detail`. `status` says each in its `attention`, a deprecation once its date has passed |
| `upgrade_available` | any command that called a server, last, at most once a day | `version`, this client's; `latest`, the latest version the server's document names, higher in a part it writes; `detail`. `status` says it in its `attention` |
| `version` | `version`, `--version` | `version`, the tag of a release or Go's pseudo-version between them; `commit`, when the build recorded it; `go`; `platform` |
| `agent`, `agent_add`, `agent_added` | `agent start`, `agent add` | `socket`, and `command`, the stock line it runs; `agent_add` also `identity_file`; `agent_added`, once the agent holds the key, `signer`, as `status` names it |
| `agent_not_added` | any command that unlocks the identity's key file under `add-keys-to-agent` | `socket`, `detail`: the agent did not accept the key, and the command went on with it, as ssh does |
| `prekey_lifetime` | `prekey lifetime` | `lifetime_seconds`, `lifetime` (the same, as a duration such as `8d`), and `detail`, a warning when the lifetime is short enough that one missed daily check-in leaves the mailbox unable to receive |
| `waiting` | any, on stderr, while it waits | `reason`: `lock`, for this mailbox's lock, which another of its commands holds; `passphrase`, for the identity's (`identity_file`); `server`, for a server answer a wait may mend, with the members of an `error` line; `connection`, for a held list that dropped, with `held_seconds`, how long it was held, `wait_seconds` and `keep_alive_seconds`; and `detail` |
| `config` | `config` | `file` and `profile`, when there is one, and `values`: each setting in force, by its key, with its `value` and `from`, where it came from |
| `config_error` | any, on stderr, where a setting is invalid, in the config file, a flag or an environment variable, and nothing ran (exit `2`) | `file`, `key` when one is at fault, `detail` |
| `usage_error` | any, on stderr, where the command line is wrong (exit `2`) | `usage`, the command's, `help`, the command that shows it, and `detail` |
| `error` | any, on stderr | `detail`; from `recv`, and from any command refused as `restore_required`, `fatal` where no wait mends the refusal (exit `4`); from a server's answer, `status`, its `code` when this client knows it, `retry_after`, and the values the code carries, each checked to its type; otherwise, from a mailbox in another format than this client's, its `code` (below), `format` and `client_format`; from a refusal with a code of its own, that `code` (below); from a locked identity, `code` `identity_locked`, `identity_file` and `agent`. One failure a line: a flush's, of several entries, has its first server answer's, and its `detail` names each. |

Other line types are named for what they report, and `allowed` and `blocked` carry `alias` as `sent` does: `status`, `unknown_sender`, `allowed`, `allowed_rebuilt`, `blocked`, `alias`, `alias_removed`, `removed` (from `refused/`), `failed_cleared` (from `inbox clear-failed`, with `count`), `prekey`, `certificates_revoked`, `no_earlier_certificate`, `account`, `revoked`, `not_revoked`, and `revocation_record`, which is the record itself, exactly the body every server accepts: save it where only you can read it (`umask 077; dromas revocation make > <file>`).

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success. Refused and held messages do not change the exit code, because any sender can cause either |
| `1` | Failure |
| `2` | Invalid command line, or an invalid setting, so nothing ran. Help that you ask for exits `0` |
| `3` | Success, and the mailbox needs attention: from `recv`, messages were received, and the prekey was not renewed, its server served a prekey the mailbox did not make (`prekey_not_made`), the mailbox's ACL was not sent to its server, the server held an ACL the mailbox had not sent, or an `--exec` program ran out of tries (`exec_failed`); from `send` and `outbox flush`, the post went ahead though the mailbox's ACL was not sent (`acl_sync_failed`); from `prekey renew`, `prekey_not_made`; from any command that called a server, a `sunset` line, or a `deprecated` line whose date has passed. Later runs retry; if `dromas status` still reports it a day later, the server is refusing the change or cannot be reached. From `status`, its `attention` list is not empty |
| `4` | From `recv`, a refusal no wait mends, and its `error` line has `fatal`: a revocation, no mailbox at this address with its registration refused, a mailbox in another format, a folder of the mailbox missing, this machine's clock behind, or any other refusal of a server's or of this client's. Under the systemd unit, which does not restart on `4`, the service stays failed until its owner puts it right and starts it again. Exit `4` also comes from any command that receives, sends, delivers or renews, `init` and `peer allow` among them, refused as `restore_required`. A process started again meets it again. Exit `1` from `recv` is a failure that a later run may pass, and `recv --follow` makes again by itself a call that a wait may mend |

## Refusal codes

The `code` member of a `refused` line or an `error` line names the check that failed. A refusal's words are the client's own, never text from the message, its sender or the server.

| Code | Check that failed |
|---|---|
| `decrypt_error` | it is not encrypted to a prekey this mailbox holds, or not to exactly one recipient of a prekey's type, as every server requires of a send |
| `malformed_message` | what was decrypted is not an envelope line and then a signature block |
| `invalid_envelope` | the envelope is not UTF-8 JSON, one object with no key twice, version 1, with a body, a `msg_id` and a `sent_at` |
| `invalid_identity` | the envelope carries no identity document, or one that breaks its format or its key checks |
| `unsupported_key_type` | the sender's identity is of a key type this client does not verify |
| `invalid_signature` | it is not signed for this mailbox by the sender's identity, or by a working key it certified under a certificate that holds now |
| `sender_mismatch` | the envelope's `from` is not the address of the identity that signed it |
| `recipient_mismatch` | the envelope's `to` is not this mailbox |
| `sender_revoked` | this mailbox holds a revocation of the sender that verifies: nothing signed for that address is accepted, whenever it was signed |
| `certificate_revoked` | it is signed under a certificate its sender's identity has revoked, by a revocation this mailbox holds that verifies: the address goes on, and nothing signed under that certificate is accepted, whenever it was signed |
| `message_expired` | its `sent_at` is older than 7 days, or before what this mailbox's replay set reaches back to |
| `message_not_yet_valid` | its `sent_at` is more than an hour ahead of this machine's clock |
| `message_already_received` | this ciphertext was already received |
| `id_mismatch` | the server's id for it is not the SHA-256 of its ciphertext |
| `unsupported_prekey_type` | a send's, in its `error` line: the recipient's prekey is an age recipient of a type this client cannot encrypt to, so nothing is sent |
| `identity_locked` | in the `error` line of a command that signs with the identity, where no source gives the key: the agent listed the key and then refused the signature, or did not answer it within 2 minutes; or the agent did not list the key, and the key file is missing, or has a passphrase that was not given or does not unlock it. Nothing is signed |
| `restore_required` | in the `error` line of any command that receives, sends, delivers or renews, `init` and `peer allow` among them, with `fatal`: `prekeys/` holds a prekey, in a file named by its serial, that `state` does not name, as after `state` is lost or put back from a copy older than a renewal. Nothing is listed, acked, sent or renewed until `dromas init --restore`; `status` says so |
| `unsupported_mailbox_format` | any command's, in its `error` line: a newer client wrote the mailbox, in a format this client does not read, so nothing of it is read or written; run a newer `dromas` |
| `mailbox_upgrade_required` | a reading's (`status`, `inbox list` and `show`, `held list`, `peer list`, `alias list`, `refused list`, `prekey lifetime`), in its `error` line: the mailbox is in an older format; `dromas recv`, or any command that writes the mailbox, brings it forward, after which an older client refuses it |

## Server errors

A server's error is an `error` line with its `status`, its `code`, the `retry_after` it asks for and the values the code carries, and a `detail`, `ERROR <status> <code>: <what the code means, in this client's words>`. None of the server's own words are printed: whoever reads them may be a model, and the server may be hostile (`PROTOCOL.md` §11.3). A code this client does not know, or one beside a `type` that is not its own, prints no `code`, and the `detail` `ERROR <status>: an answer this client does not know`.
