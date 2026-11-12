## dromas recv

receive, verify and acknowledge every message, one JSON line each

### Synopsis

Receive, verify and acknowledge every message. recv prints a "message" line for
each message, built from the verified fields, with this mailbox's name for the
sender as "alias" when you named one (dromas alias add). It prints a "refused"
line for each message it refused, keeping the message in refused/, and a "held"
line for each message it held, keeping the message in held/. recv also renews
the prekey once a day, so run it at least every 6d22h and the mailbox never
lapses.

With --headers, a "message_headers" line takes the place of each "message" line.
Its "envelope" has the fields this client checked, with none of the text the
sender chose (no body and no subject), and the line has the body's size. The
--exec program still gets each "message" line. Each message is kept in inbox/ as
always, and dromas inbox show \<id> prints its "message" line, so a reader can
decide by sender before reading what the sender wrote.

One drain runs for at most --drain-time, holding the mailbox's lock. At that
limit, recv stops with a "more" line, as at its other limits, and keeps what it
read of the message it was fetching, which the next recv continues from.

With --wait, recv holds the mailbox's list open between drains, and ends at its
first delivery or at the end of the duration. With --follow, recv continues
after each delivery, without end, or until the end of the --wait duration when
both are given: run it in the foreground under a service manager. An interrupt
or a SIGTERM ends --follow with exit status 0. The mailbox's lock stays free
between drains, so the mailbox's other commands can run meanwhile. With --wait
or --follow, recv makes a call again when a wait may fix it, and reports each
wait on stderr ("waiting").

With --log-file, recv appends each line it would print, on stdout and on stderr,
to the file instead, the "error" line it ends with included. A service manager
that keeps no log of its own, as Windows' Task Scheduler, otherwise keeps none
of them. recv makes the file readable by its owner alone when the file is not
there, and leaves a file that is there as it is. Nothing rotates the file.

Exit status 4, with "fatal" in the "error" line, means that no wait fixes the
refusal: a revocation, no mailbox at this address with its registration refused,
a mailbox in another format, a missing folder of the mailbox, as on an unmounted
volume, this machine's clock behind, or any other refusal by the server or by
this client. A service manager leaves such a service failed until its owner
fixes the cause and starts it again. Exit status 1 is any other failure.

With --exec, recv runs a program for each message delivered to inbox/, with the
message's "message" line on stdin, as recv prints it without --headers. --exec
needs --wait or --follow. --exec names the program, and the arguments after --
go to it one for one: no shell runs, and nothing is split or expanded. The
config file's [recv] exec names the program and its arguments as one list, for
example exec = ["/usr/local/bin/notify", "--quiet"]. recv runs that program only
with --wait or --follow, and only when the command line has no --exec, which
replaces it. The file's [recv] exec-timeout sets the default of --exec-timeout,
and the file can set it only beside exec. On Windows, cmd.exe runs a .bat or
.cmd program, and cmd.exe splits and expands its arguments.

Nothing of a message is ever on the command line. recv discards the program's
stdout and stderr. The programs run one at a time, in delivery order, with the
mailbox's lock free, so a program can run dromas. Each message is kept in inbox/
first, so the program is a notification and never the only copy. A message held
in held/ runs no program, and neither does a message that another recv receives,
so run one recv for each mailbox.

A program's exit status 0 means done, and 100 means failed for good. recv tries
again after any other exit status, a signal, a run longer than --exec-timeout
(which kills the process), or a program that does not start, up to 3 tries in
all, 30s apart. Each failed attempt prints an "exec_attempt_failed" line, and a
message with no attempts left an "exec_failed" line, which dromas status then
lists under attention until dromas inbox clear-failed clears it. recv then
continues with the next message, and exits with status 3 at its end.

Nothing runs that message's program again: dromas inbox show \<id> | \<program>
hands it to a program by hand. A process killed before a program's tries end
runs that program again when recv --exec next starts, so the program runs at
least once for each message and may run twice: it should use the "id" as the
message's name.

```
dromas recv [flags] [-- <argument>...]
```

### Options

```
      --drain-time duration     how long one drain runs before it stops for the next recv (default 15m0s)
      --exec program            run this program for each message, with its line on stdin, and the arguments after -- as its own
      --exec-timeout duration   kill --exec's program when one run takes this long (default 5m0s)
      --follow[=true|false]     continue after each message, without end or for --wait's duration
      --headers[=true|false]    print each message's checked fields and its body's size, without its body or subject
      --help[=true|false]       print this help, and run nothing
      --log-file file           append every line recv prints, stdout's and stderr's, to this file
      --wait duration           hold the list open until a message arrives, for at most this long, however many held requests that needs
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

