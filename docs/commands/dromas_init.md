## dromas init

create this mailbox and register it; prints its link

### Synopsis

Create this mailbox and register it, and print its address and its link,
\<server>/to/\<address>, as addr does. Its identity signs what it says. With
--offline, its identity certifies a working key with each prekey, for as long
as its server takes one, and the working key signs: for good, since its
server and its correspondents then take no statement its identity signs. Run
with an identity file and nothing else of the mailbox, init restores that
mailbox, under a certificate where its server serves a statement under one or
--offline asks. A sender that saw this address under a certificate refuses one
its identity signs: where the server has forgotten a mailbox that was under
certificates, run dromas cert switch \<address> next. A profile with no config
file gets one, each path in full. With --revocation-file, init also writes the
mailbox's revocation record, as dromas revocation make prints it, to a new file
outside every directory of the mailbox, for the day the mailbox is lost: keep
it where only you can read it. On a mailbox already registered, init with
--revocation-file writes the record and changes nothing else.

A mailbox whose state does not match its prekeys/, as after state is lost or
put back from an older copy with prekeys/ kept, is a failure: it receives,
sends, delivers and renews nothing, init and peer allow among them, until
init --restore acknowledges it. That adopts the prekeys the state does not
name and renews. The lost state took its replay set with it, so a message
the mailbox accepted before may be delivered again. On a mailbox whose state
matches, init --restore changes nothing, so it may run at every start. A
mailbox restored from its identity alone needs no --restore.

```
dromas init [flags]
```

### Options

```
      --help[=true|false]      print this help, and run nothing
      --identity-type type     make the identity a key of this type, not ssh-ed25519: ecdsa-sha2-nistp256, which every client verifies too
      --offline[=true|false]   register the mailbox under a certificate, whose working key signs in place of its identity: for good
      --restore[=true|false]   acknowledge a state that does not match prekeys/, as after state is lost or put back from an older copy, so the mailbox runs again: on one that matches, it changes nothing
      --revocation-file file   write this mailbox's revocation record, as dromas revocation make prints it, to this new file, outside every directory of the mailbox: $DROMAS_REVOCATION_FILE
```

### Options inherited from parent commands

```
      --add-keys-to-agent time                  once a passphrase unlocks the identity's key, add it to the agent: no, yes, or the time it stays, as ssh_config's AddKeysToAgent: $DROMAS_ADD_KEYS_TO_AGENT
      --agent socket                            the socket of the ssh-agent that holds the identity's key, read as ssh_config's IdentityAgent (a path, $VAR, SSH_AUTH_SOCK or none): $DROMAS_AGENT, or $XDG_RUNTIME_DIR/dromas/agent.sock, or dromas/agent.sock in the user's cache directory, and none on Windows
      --askpass program                         a program that prints the identity key's passphrase, as ssh's SSH_ASKPASS: $DROMAS_ASKPASS
      --base-dir directory                      the directory of the mailbox's state, and the default of each path below
      --ca-file file                            trust only the certificates in this PEM file for the server's TLS certificate, in place of the system's CAs: $DROMAS_CA_FILE
      --certificate-lifetime time               how long each certificate this mailbox makes runs, valid before less valid after: a time above 7d1h and at most 45d1h, as in 30d: $DROMAS_CERTIFICATE_LIFETIME, or the longest its server takes
      --config file                             read this config file: $DROMAS_CONFIG, or --profile's
      --identity-file file                      the identity key file: <base-dir>/identity
      --inbox-dir directory                     the directory of inbox/, apart from the rest of the mail: <mail-dir>/inbox
      --mail-dir directory                      the directory of inbox/, held/, refused/ and sent/
      --no-prompt[=true|false]                  never ask for the identity key's passphrase at a terminal: $DROMAS_NO_PROMPT
      --prekey-lifetime time                    how long each new prekey is valid for: a time, as dromas prekey lifetime --help says
      --profile profile                         read this profile's config file, in the config directory: $DROMAS_PROFILE, or default
      --rotating-keys-dir directory             the directory of prekeys/, work/ and tls-ticket, never to be backed up: <base-dir>/keys
      --server URL                              the server's URL: $DROMAS_SERVER_URL, or https://dromas.dev
      --unsafe-inbox-permissions[=true|false]   read inbox-dir and held/ where others can write them, and the messages they keep where others can reach them, though whoever can write there can put in a message that reads as delivered: $DROMAS_UNSAFE_INBOX_PERMISSIONS
      --unsafe-state-permissions[=true|false]   open state, gate, partial and the lists where others can reach them or another user owns them; never a private key: $DROMAS_UNSAFE_STATE_PERMISSIONS
```

### SEE ALSO

* [dromas](dromas.md)	 - 

