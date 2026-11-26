## dromas inbox

messages recv delivered: list, show, rm, retry-failed, clear-failed

### Synopsis

recv keeps each message it delivers as a file in inbox/, holding the "message"
line it printed. dromas deletes these files only when you run dromas inbox rm:
managing them is otherwise up to you.

These commands list those messages and print their lines again, delete them,
or hand the messages whose --exec program or webhook gave up to the next recv,
or clear what dromas status reports of them. They call no server.

```
dromas inbox
```

### Options

```
      --help[=true|false]   print this help, and run nothing
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
      --unsafe-inbox-permissions[=true|false]   run on the mailbox even when others can write to its directories, its outbox entries or the messages inbox-dir and held/ keep, or another user owns one, although anyone who can write there can add a file the mailbox takes as its owner's, such as a message it signs and sends or one that reads as delivered: $DROMAS_UNSAFE_INBOX_PERMISSIONS
      --unsafe-state-permissions[=true|false]   open state, gate and partial even when others can access them, and the lists even when others can write to them, or another user owns one; never a private key: $DROMAS_UNSAFE_STATE_PERMISSIONS
```

### SEE ALSO

* [dromas](dromas.md)	 - 
* [dromas inbox clear-failed](dromas_inbox_clear-failed.md)	 - clear what status reports of every --exec program and webhook that gave up
* [dromas inbox list](dromas_inbox_list.md)	 - list the messages in inbox/, one headers line each
* [dromas inbox retry-failed](dromas_inbox_retry-failed.md)	 - have the next recv run each --exec program and post each webhook that gave up again
* [dromas inbox rm](dromas_inbox_rm.md)	 - delete messages from inbox/
* [dromas inbox show](dromas_inbox_show.md)	 - print a message in inbox/, as recv printed it

