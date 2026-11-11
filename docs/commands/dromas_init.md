## dromas init

create this mailbox and register it, and print its link

### Synopsis

Create this mailbox and register it, and print its address and link,
\<server>/to/\<address>, as addr does. The mailbox's identity signs what the
mailbox says.

With --offline, the identity certifies a working key with each prekey, for as
long as the server accepts one, and the working key signs. This is permanent,
because the server and the mailbox's correspondents then accept no statement
that the identity signs.

Run with an identity file and nothing else of the mailbox, init restores that
mailbox, under a certificate when the server serves a statement under one or
--offline asks for one. A sender that saw this address under a certificate
refuses a statement its identity signs: when the server has forgotten a mailbox
that was under certificates, run dromas cert switch \<address> next.

A profile without a config file gets one, with each path in full.

With --revocation-file, init also writes the mailbox's revocation record, as
dromas revocation make prints it, to a new file outside every directory of the
mailbox, for the day the mailbox is lost: keep the file where only you can read
it. On a mailbox already registered, init with --revocation-file writes the
record and changes nothing else.

A mailbox whose state does not match its prekeys/, as after state is lost, or
restored from an older copy while prekeys/ was kept, has failed: it receives,
sends, delivers and renews nothing, init and peer allow included, until init
--restore acknowledges the mismatch. init --restore adopts the prekeys the state
does not name, and renews. The lost state took its replay set with it, so the
mailbox may deliver again a message it accepted before. On a mailbox whose state
matches, init --restore changes nothing, so it can run at every start. A mailbox
restored from its identity alone needs no --restore.

```
dromas init [flags]
```

### Options

```
      --help[=true|false]      print this help, and run nothing
      --identity-type type     make the identity a key of this type, not ssh-ed25519: ecdsa-sha2-nistp256, which every client verifies too
      --offline[=true|false]   register the mailbox under a certificate, whose working key signs in place of its identity: permanent
      --restore[=true|false]   acknowledge a state that does not match prekeys/, as after state is lost or restored from an older copy, so the mailbox runs again; on a state that matches, it changes nothing
      --revocation-file file   write this mailbox's revocation record, as dromas revocation make prints it, to this new file, outside every directory of the mailbox: $DROMAS_REVOCATION_FILE
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

* [dromas](dromas.md)	 - 

