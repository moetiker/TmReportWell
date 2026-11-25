---
title: TMWELL
section: 1
header: tmwell manual
footer: tmwell
date: 2026-11-25
---

# NAME

tmwell - mail a daily report about Time Machine

# SYNOPSIS

**tmwell** *command*

# CONFIGURATION

The configuration is a JSON file, see **FILES**.

- `smtp_host`: Name of the mail server.
- `smtp_port`: Port of the mail server, 587 by default.
  Port 465 uses TLS from the start, every other port uses STARTTLS.
- `smtp_user`: Login name on the mail server and account of the keychain item.
- `from`: Sender address.
- `to`: Recipient address.
- `send_at`: Time of day after which the report is due, 09:00 by default.
- `warn_days`: Age in days of the last completed backup at which the report reads WARNING, 2 by default.
- `problem_days`: Age in days of the last completed backup at which the report reads PROBLEM, 7 by default.

The password is the generic keychain item with the service `tmwell-smtp` and the account `smtp_user`.

# DESCRIPTION

**tmwell** reports which Time Machine backups completed since the last report,
which did not run or did not complete,
and the reasons given in the system log.

Backups come from the Time Machine preferences.
Log messages of **backupd** and **backupd-helper** that differ only in numbers are grouped.
A message that **tmwell** does not recognise appears verbatim in the report.

The subject reads OK, WARNING or PROBLEM.
WARNING also covers errors in the log, a log that cannot be read, backups that did not complete and automatic backups that are switched off.
PROBLEM also covers Time Machine preferences that cannot be read, no configured backup disk and no completed backup on record.

The launch agent runs **tmwell run** every hour.
A report that cannot be mailed is sent by the next run that succeeds.

# OPTIONS

- `report`: Print the report. Sends nothing and changes nothing.
- `run`: Collect new log messages and mail the report if it is due.
- `send`: Collect new log messages and mail the report now.
- `install`: Install and start the launch agent.
- `uninstall`: Stop and remove the launch agent.
- `-V, --version`: Print the version and exit.

# EXIT STATUS

- `0`: Success.
- `1`: The command failed. The reason is on standard error.
- `2`: The command line is wrong.

# FILES

- `~/Library/Application Support/TmWell/config.json`: The configuration.
- `~/Library/Application Support/TmWell/state.json`: Collected log messages and the time of the last report.
- `~/Library/LaunchAgents/tmwell.plist`: The launch agent.
- `~/Library/Logs/tmwell.log`: Errors of the launch agent.
- `/Library/Preferences/com.apple.TimeMachine.plist`: The Time Machine preferences.

# CAVEATS

macOS can deny access to the Time Machine preferences.
The report then reads "backup state unknown", and **tmwell** needs Full Disk Access in the system settings.

# AUTHORS

Manuel Oetiker <manuel@oetiker.ch>

# REPORTING BUGS

<https://github.com/moetiker/TmWell/issues>

# COPYRIGHT

Copyright (c) 2026 Manuel Oetiker.
Released under the MIT licence, see
<https://github.com/moetiker/TmWell/blob/main/LICENSE>.

# SEE ALSO

Project home: <https://github.com/moetiker/TmWell>

**tmutil**(8), **log**(1), **launchctl**(1)
