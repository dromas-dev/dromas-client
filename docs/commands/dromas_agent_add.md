## dromas agent add

load the identity's key into the mailbox's agent, with ssh-add

### Synopsis

Run stock ssh-add on the identity key file, against the socket that --agent
names, with this command's stdin: SSH_AUTH_SOCK=\<socket> ssh-add
\<identity-file>.

Without --askpass, ssh-add asks for the key's passphrase at the terminal and
runs no askpass program (SSH_ASKPASS_REQUIRE=never). When the key file has a
passphrase that ssh-add cannot ask for, because stdin is not a terminal or
--no-prompt is set, agent add fails with "identity_locked" before ssh-add runs.

With --askpass, ssh-add runs that program through dromas (SSH_ASKPASS=\<dromas>
SSH_ASKPASS_REQUIRE=force DROMAS_ASKPASS=\<program>), and dromas gives ssh-add no
passphrase after the third, as ssh's NumberOfPasswordPrompts does. When no
passphrase from the askpass program unlocks the key file, agent add fails with
"identity_locked". Any other ssh-add failure is reported with what ssh-add wrote
to stderr.

agent add refuses a revoked mailbox, which signs nothing more, before ssh-add
runs. Before ssh-add runs, an "agent_add" line gives the command it runs. Once
the agent holds the key, an "agent_added" line gives the mailbox's signer, as
status names it.

--lifetime is ssh-add -t: when it ends, the agent drops the key. While the agent
lacks the key and the key file has a passphrase, a mailbox whose identity signs
neither sends nor receives, and a mailbox under a certificate renews nothing,
except in a command run with --askpass or at a terminal.

```
dromas agent add [flags]
```

### Options

```
      --help[=true|false]   print this help, and run nothing
      --lifetime time       how long the agent keeps the key, as ssh-add -t: a time
```

### Options inherited from parent commands

```
      --add-keys-to-agent time                  after a passphrase unlocks the identity's key, add the key to the agent: no, yes, or the time the agent keeps it, as ssh_config's AddKeysToAgent: $DROMAS_ADD_KEYS_TO_AGENT
      --agent socket                            the socket of the ssh-agent that holds the identity's key, read as ssh_config's IdentityAgent (a path, $VAR, SSH_AUTH_SOCK or none): $DROMAS_AGENT, or $XDG_RUNTIME_DIR/dromas/agent.sock, or dromas/agent.sock in the user's cache directory, and none on Windows
      --askpass program                         a program that prints the identity key's passphrase, as ssh's SSH_ASKPASS: $DROMAS_ASKPASS
      --base-dir directory                      the directory of the mailbox's state, and the default of each path below
      --ca-file file                            trust only the certificates in this PEM file for the server's TLS certificate, in place of the system's CAs: $DROMAS_CA_FILE
      --certificate-lifetime time               how long each certificate this mailbox makes is valid (valid before minus valid after): a time above 7d1h and at most 45d1h, as in 30d: $DROMAS_CERTIFICATE_LIFETIME, or the longest the server accepts
      --config file                             read this config file: $DROMAS_CONFIG, or --profile's
      --identity-file file                      the identity key file: <base-dir>/identity
      --inbox-dir directory                     the directory of inbox/, apart from the mailbox's other messages: <mail-dir>/inbox
      --mail-dir directory                      the directory of inbox/, held/, refused/ and sent/
      --no-prompt[=true|false]                  never ask for the identity key's passphrase at a terminal: $DROMAS_NO_PROMPT
      --prekey-lifetime time                    how long each new prekey is valid: a time, as dromas prekey lifetime --help describes
      --profile profile                         read this profile's config file, in the config directory: $DROMAS_PROFILE, or default
      --rotating-keys-dir directory             the directory of prekeys/, work/ and tls-ticket, never to be backed up: <base-dir>/keys
      --server URL                              the server's URL: $DROMAS_SERVER_URL, or https://dromas.dev
      --unsafe-inbox-permissions[=true|false]   read inbox-dir and held/ even when others can write to them or another user owns them, and their messages even when others can access them, although anyone who can write there can add a message that reads as delivered: $DROMAS_UNSAFE_INBOX_PERMISSIONS
      --unsafe-state-permissions[=true|false]   open state, gate, partial and the lists even when others can access them or another user owns them; never a private key: $DROMAS_UNSAFE_STATE_PERMISSIONS
```

### SEE ALSO

* [dromas agent](dromas_agent.md)	 - the mailbox's own ssh-agent for the identity's key: start, add

