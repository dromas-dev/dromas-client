## dromas send

send a message: the body from a file, or stdin

### Synopsis

Send one message to a link, a bare address or an alias, with the body
from the file, or from stdin, and print the server's id for it and its msg_id.
It waits in outbox/ until a server takes it (dromas outbox flush posts it
again), and is then kept in sent/ with the server's receipt. Sending to a mailbox
allows it, so a message that talks you into a send also lets that address's
messages past held/: decide a send as you would dromas peer allow.

The body is text, unless --json says it is one JSON value: then an object
arrives as that object, not as a string. One its reader would refuse, a name
twice in an object among them, is refused, and nothing is sent.

```
dromas send [flags] <link, address or alias> [file]
```

### Options

```
      --help[=true|false]    print this help, and run nothing
      --in-reply-to msg_id   the msg_id of the message this answers, as recv printed it
      --json[=true|false]    the body is one JSON value, sent as that value and not as text
      --subject text         the message's subject: any text
      --supersedes msg_id    the msg_id of an earlier message of yours that this replaces, as sent/ names it
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

