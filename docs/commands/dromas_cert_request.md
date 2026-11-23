## dromas cert request

make a new working key for the identity's holder to certify

### Synopsis

Make a new working key, and print the request for its certificate: one
"certificate_request" line. Save the line's working key as request.pub in the
directory of the identity key file, run the line's command there, with the key
file's own name in place of identity, and bring the certificate it writes,
request-cert.pub, back to dromas cert install.

The certificate's serial is at least the request's lowest_serial, and the
certificate runs for at most max_certificate_lifetime_seconds. A request made
later replaces this one.

The mailbox renews under the certificate it holds until then. Give it the next
certificate before its current one has 7 days left, because no message is
signed under a certificate with less: dromas status gives that time as
certificate_signs_messages_until.

```
dromas cert request
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
      --unsafe-inbox-permissions[=true|false]   read base-dir, mail-dir, outbox/, inbox-dir and held/, and the messages of the last two, even when others can write to them or another user owns them, although anyone who can write there can add a file the mailbox takes as its owner's, such as a message that reads as delivered: $DROMAS_UNSAFE_INBOX_PERMISSIONS
      --unsafe-state-permissions[=true|false]   open state, gate and partial even when others can access them, and the lists even when others can write to them, or another user owns one; never a private key: $DROMAS_UNSAFE_STATE_PERMISSIONS
```

### SEE ALSO

* [dromas cert](dromas_cert.md)	 - this mailbox's certificates: switch, request, install, revoke-earlier, publish-revoked

