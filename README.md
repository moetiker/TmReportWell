# TmWell

[![CI](https://github.com/moetiker/TmWell/actions/workflows/ci.yml/badge.svg)](https://github.com/moetiker/TmWell/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/moetiker/TmWell)](https://github.com/moetiker/TmWell/releases/latest)

TmWell mails one report per day about Time Machine on a Mac: which backups
completed, which did not run, and the reason the system log gives. The
[manual](docs/manual.md) has the details.

## Install

mise:

```sh
mise use -g github:moetiker/TmWell
```

Direct download: <https://github.com/moetiker/TmWell/releases/latest>

## Set up

Create the configuration and fill in the mail server:

```sh
mkdir -p ~/Library/Application\ Support/TmWell
cp config.example.json ~/Library/Application\ Support/TmWell/config.json
open -e ~/Library/Application\ Support/TmWell/config.json
```

Store the SMTP password in the login keychain. The account is the `smtp_user`
from the configuration, and the command asks for the password:

```sh
security add-generic-password -s tmwell-smtp -a <smtp_user> -w
```

Send a first report, then install the launch agent:

```sh
tmwell send
tmwell install
```

If the mailed report says that the Time Machine preferences could not be read,
add `tmwell` under System Settings, Privacy & Security, Full Disk
Access.

## Commands

- `tmwell report`: Print the report. Sends nothing and changes nothing.
- `tmwell run`: Collect new log messages and mail the report if it is due.
- `tmwell send`: Collect new log messages and mail the report now.
- `tmwell install`: Run `tmwell run` every hour as a launch agent.
- `tmwell uninstall`: Remove the launch agent.
- `tmwell --version`: Print the version.

## Configuration

- `smtp_host`, `smtp_port`, `smtp_user`: Mail server and login. Port 465 uses
  TLS from the start, every other port uses STARTTLS.
- `from`, `to`: Sender and recipient address.
- `send_at`: Time of day for the report, 09:00 by default. If the Mac is asleep
  or the mail server is unreachable then, the report follows at the next hourly
  run that succeeds.
- `warn_days`, `problem_days`: Age of the last completed backup that makes the
  report a WARNING (2) or a PROBLEM (7).

Errors of the launch agent go to `~/Library/Logs/tmwell.log`.
