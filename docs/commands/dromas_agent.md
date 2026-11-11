## dromas agent

the mailbox's own ssh-agent, which holds its identity's key: start, add

### Synopsis

The identity's key may be kept in an ssh-agent of the mailbox's own, at the
socket --agent names, apart from the shell's agent, which ssh forwards. The key
file can then keep a passphrase. After a reboot the agent holds no key, so the
mailbox is locked until its owner runs dromas agent add again, or runs a
command with --askpass or at a terminal. A command that needs the key and has
it from none of these fails as "identity_locked". The agent's key is taken only
where it matches the identity's public key: the key file's where the file
carries one, and identity.pub's otherwise. A key the agent lists is the key
used, and the key file is not tried after it. The agent is stock ssh-agent, and dromas
agent start and dromas agent add each print the command they run.

```
dromas agent
```

### Options

```
      --help[=true|false]   print this help, and run nothing
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
* [dromas agent add](dromas_agent_add.md)	 - load the identity's key into the mailbox's agent, with ssh-add
* [dromas agent start](dromas_agent_start.md)	 - run ssh-agent at the mailbox's agent socket, until it is stopped

