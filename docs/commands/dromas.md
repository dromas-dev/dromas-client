## dromas



```
dromas
```

### Options

```
      --add-keys-to-agent time                  after a passphrase unlocks the identity's key, add the key to the agent: no, yes, or the time the agent keeps it, as ssh_config's AddKeysToAgent: $DROMAS_ADD_KEYS_TO_AGENT
      --agent socket                            the socket of the ssh-agent that holds the identity's key, read as ssh_config's IdentityAgent (a path, $VAR, SSH_AUTH_SOCK or none): $DROMAS_AGENT, or $XDG_RUNTIME_DIR/dromas/agent.sock, or dromas/agent.sock in the user's cache directory, and none on Windows
      --askpass program                         a program that prints the identity key's passphrase, as ssh's SSH_ASKPASS: $DROMAS_ASKPASS
      --base-dir directory                      the directory of the mailbox's state, and the default of each path below
      --ca-file file                            trust only the certificates in this PEM file for the server's TLS certificate, in place of the system's CAs: $DROMAS_CA_FILE
      --certificate-lifetime time               how long each certificate this mailbox makes is valid (valid before minus valid after): a time above 7d1h and at most 45d1h, as in 30d: $DROMAS_CERTIFICATE_LIFETIME, or the longest the server accepts
      --config file                             read this config file: $DROMAS_CONFIG, or --profile's
      --help[=true|false]                       print this help, and run nothing
      --identity-file file                      the identity key file: <base-dir>/identity
      --inbox-dir directory                     the directory of inbox/, apart from the mailbox's other messages: <mail-dir>/inbox
      --mail-dir directory                      the directory of inbox/, held/, refused/ and sent/
      --no-prompt[=true|false]                  never ask for the identity key's passphrase at a terminal: $DROMAS_NO_PROMPT
      --prekey-lifetime time                    how long each new prekey is valid: a time, as dromas prekey lifetime --help describes
      --profile profile                         read this profile's config file, in the config directory: $DROMAS_PROFILE, or default
      --rotating-keys-dir directory             the directory of prekeys/, work/ and tls-ticket, which need no backup: <base-dir>/keys
      --server URL                              the server's URL: $DROMAS_SERVER_URL, or https://dromas.dev
      --unsafe-inbox-permissions[=true|false]   read inbox-dir and held/ even when others can write to them or another user owns them, and their messages even when others can access them, although anyone who can write there can add a message that reads as delivered: $DROMAS_UNSAFE_INBOX_PERMISSIONS
      --unsafe-state-permissions[=true|false]   open state, gate, partial and the lists even when others can access them or another user owns them; never a private key: $DROMAS_UNSAFE_STATE_PERMISSIONS
      --version[=true|false]                    print this client's version, as dromas version does
```

### SEE ALSO

* [dromas account](dromas_account.md)	 - the account that can lift this mailbox's limits: join, leave
* [dromas addr](dromas_addr.md)	 - print this mailbox's link, to give to whoever will write to it
* [dromas agent](dromas_agent.md)	 - the mailbox's own ssh-agent for the identity's key: start, add
* [dromas alias](dromas_alias.md)	 - names for peers, usable in place of a link: add, rm, list
* [dromas cert](dromas_cert.md)	 - this mailbox's certificates: switch, revoke-earlier, publish-revoked
* [dromas config](dromas_config.md)	 - print every setting and where it came from
* [dromas held](dromas_held.md)	 - messages from unknown senders, held to allow or block: list
* [dromas inbox](dromas_inbox.md)	 - messages recv delivered: list, show, clear-failed
* [dromas init](dromas_init.md)	 - create this mailbox and register it, and print its link
* [dromas outbox](dromas_outbox.md)	 - messages the server has not accepted yet: flush
* [dromas peer](dromas_peer.md)	 - who can write to this mailbox: allow, block, list
* [dromas prekey](dromas_prekey.md)	 - the key that senders encrypt to: renew, lifetime
* [dromas recv](dromas_recv.md)	 - receive, verify and acknowledge every message, one JSON line each
* [dromas refused](dromas_refused.md)	 - messages that failed a check: list, rm
* [dromas revocation](dromas_revocation.md)	 - permanently ending an address: make, publish, keep, ask
* [dromas send](dromas_send.md)	 - send a message to one mailbox or a group, with the body from stdin or a file
* [dromas status](dromas_status.md)	 - report what this mailbox holds and what needs attention
* [dromas version](dromas_version.md)	 - print this client's version, commit, Go release and platform

