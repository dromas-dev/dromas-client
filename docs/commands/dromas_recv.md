## dromas recv

receive, verify and ack every message: one line of JSON each

### Synopsis

Receive, verify and ack every message: a "message" line for each, built from the
verified fields, with this mailbox's name for its sender as "alias" when it
named one (dromas alias add). It also renews the prekey once a day, so run it at least
every 6d22h and the mailbox never lapses. It prints a "refused" line for each message
it refused, keeping it in refused/, and a "held" line for each it held,
keeping it in held/.

With --headers, a "message_headers" line takes each "message" line's place: its
"envelope" has the fields this client checked with none of the text the
sender chose, no body and no subject, and the line has the body's size.
--exec's program still gets each "message" line. Each message is kept in
inbox/ as ever, and dromas inbox show \<id> prints its
"message" line, so a reader can decide by sender before it reads what the sender wrote.

One drain runs for at most --drain-time, holding the mailbox's lock: there it
stops with a "more" line, as at its other limits, and keeps what it read of
the message it was fetching, which the next recv goes on from.

With --wait, recv holds the mailbox's list between drains, and ends at its first
delivery or at the end of the duration. With --follow, recv goes on after each
delivery, without end, or until the end of --wait's duration where both are
given: run one in the foreground under a service manager. An interrupt or a
SIGTERM ends --follow with exit status 0. The mailbox's lock stays free
between drains, so its other commands run meanwhile. A call that may mend is
made again, each said on stderr ("waiting"). Exit status 4 and "fatal" in
the "error" line say that no wait mends the refusal: a revocation, no mailbox
at this address with its registration refused, a mailbox in another format, a
folder of the mailbox missing, as on a volume not mounted, this machine's
clock behind, or any other refusal of the server's or of this client's. A
service manager leaves such a service failed until its owner puts it right
and starts it again. Exit status 1 is any other failure.

With --exec, recv runs a command for each message that lands in inbox/, with
the message's "message" line on stdin, as recv prints it without --headers. It takes --wait or --follow.
--exec names the program, and the arguments after -- go to it one for one: no
shell runs, and nothing is split or expanded. The config file's [recv] exec
names the program and its arguments as one list, such as exec =
["/usr/local/bin/notify", "--quiet"]. recv runs that program only with
--wait or --follow, and only where the command line gives no --exec, which
replaces it. The file's [recv] exec-timeout is --exec-timeout's default, and
the file sets it only beside exec. On Windows, cmd.exe runs a .bat
or .cmd program, and cmd.exe splits and expands those arguments. Nothing of a
message is ever on the command line. The program's stdout and stderr
are discarded. The commands run one at a time, in delivery order, with the
mailbox's lock free, so a command may run dromas. Each message is kept in
inbox/ first, so a command is a notification and never the only copy. A message
held in held/ runs no command, and neither does one another recv takes, so
run one recv for each mailbox.

Exit status 0 is done, and 100 is failed for good. Any other exit status, a
signal, a run longer than --exec-timeout (which kills the process) or a
program that does not start is tried again, up to 3 tries in all, 30s apart.
Each failed attempt prints an "exec_attempt_failed" line, and a message with no attempt left
an "exec_failed" line, which dromas status then lists under attention until
dromas inbox clear-failed clears it, and recv goes on to the next message and
exits with status 3 at its end. Nothing runs that message's command again:
dromas inbox show \<id> | \<program> hands it to a program by hand. A
process killed before a command's tries end runs that command again when recv
--exec next starts, so a command runs at least once for each message and
may run twice: it should take the "id" as the message's name.

```
dromas recv [flags] [-- <argument>...]
```

### Options

```
      --drain-time duration     how long one drain runs before it stops for the next recv (default 15m0s)
      --exec program            run this program for each message, with its line on stdin, and the arguments after -- as its own
      --exec-timeout duration   kill --exec's program when one run takes this long (default 5m0s)
      --follow[=true|false]     go on after each message, without end or for --wait's duration
      --headers[=true|false]    print each message's checked fields and its body's size, without its body or subject
      --help[=true|false]       print this help, and run nothing
      --wait duration           hold the list until a message arrives, for at most this long, however many held requests it takes
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

