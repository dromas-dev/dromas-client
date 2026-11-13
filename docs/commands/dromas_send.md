## dromas send

send a message to one mailbox or a group, with the body from stdin or a file

### Synopsis

Send one message to a link, a bare address or an alias, with the body from
stdin, or from the file --file names, and print the server's id for it and its
msg_id. The message waits in outbox/ until a server accepts it (dromas outbox
flush posts it again, and dromas recv --follow posts it once the server's
Retry-After for it has passed), and is then kept in sent/ with the server's
receipt.

Name several recipients, or --reply-all with the id of a message in inbox/, and
the message goes to a group: every recipient gets a message of its own, each
with the same msg_id and with every member's address in its group, this
mailbox's among them. The group is its members: a reply to every member of a
group is a message to the same group, and adding or leaving out a member makes
another group. send prints a "sent" line for each member the server accepts, and
a "queued" line, with when it is due, for each that waits: where this mailbox's
budget of sends at its server does not hold the members still to go, they wait
together, and the exit status is 3. A group needs one send for each member but
this mailbox, so a group larger than that budget holds at once is refused, with
nothing written.

Sending to a mailbox allows it, so a message that talks you into a send also
lets that address's messages past held/: decide a send as you would decide
dromas peer allow.

The body is text, unless --json says it is one JSON value: then an object
arrives as that object, not as a string. send refuses a JSON value its reader
would refuse, an object with a name twice among them, and sends nothing.

```
dromas send [flags] <link, address or alias>...
```

### Options

```
      --file path            the path of a file whose bytes are the body, in place of stdin
      --help[=true|false]    print this help, and run nothing
      --in-reply-to msg_id   the msg_id of the message this answers, as recv printed it
      --json[=true|false]    the body is one JSON value, sent as that value and not as text
      --reply-all id         the id of a message in inbox/, as recv printed it: send to its sender and every other member of its group, in reply to it
      --subject text         the message's subject: any text
      --supersedes msg_id    the msg_id of an earlier message of yours that this replaces, as sent/ names it
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
      --rotating-keys-dir directory             the directory of prekeys/, work/ and tls-ticket, which need no backup: <base-dir>/keys
      --server URL                              the server's URL: $DROMAS_SERVER_URL, or https://dromas.dev
      --unsafe-inbox-permissions[=true|false]   read inbox-dir and held/ even when others can write to them or another user owns them, and their messages even when others can access them, although anyone who can write there can add a message that reads as delivered: $DROMAS_UNSAFE_INBOX_PERMISSIONS
      --unsafe-state-permissions[=true|false]   open state, gate, partial and the lists even when others can access them or another user owns them; never a private key: $DROMAS_UNSAFE_STATE_PERMISSIONS
```

### SEE ALSO

* [dromas](dromas.md)	 - 

