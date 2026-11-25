# TmReportWell

[![CI](https://github.com/moetiker/TmReportWell/actions/workflows/ci.yml/badge.svg)](https://github.com/moetiker/TmReportWell/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/moetiker/TmReportWell)](https://github.com/moetiker/TmReportWell/releases/latest)

TmReportWell mails one report per day about Time Machine on a Mac: which backups
completed, which did not run, and the reason the system log gives. The
[manual](docs/manual.md) has the details.

## Install

Homebrew:

```sh
brew tap moetiker/TmReportWell https://github.com/moetiker/TmReportWell
brew trust --formula moetiker/TmReportWell/tmreportwell
brew install tmreportwell
```

`brew trust` exists from Homebrew 6 on; skip it on older versions.

mise:

```sh
mise use -g github:moetiker/TmReportWell
```

Direct download: <https://github.com/moetiker/TmReportWell/releases/latest>

## Set up

Create the configuration from
[config.example.json](config.example.json) and fill in the mail server:

```sh
mkdir -p ~/Library/Application\ Support/TmReportWell
curl -fsSL -o ~/Library/Application\ Support/TmReportWell/config.json https://raw.githubusercontent.com/moetiker/TmReportWell/main/config.example.json
open -e ~/Library/Application\ Support/TmReportWell/config.json
```

Store the SMTP password in the login keychain. The account is the `smtp_user`
from the configuration, and the command asks for the password:

```sh
security add-generic-password -s tmreportwell-smtp -a <smtp_user> -w
```

Send a first report, then install the launch agent:

```sh
tmreportwell send
tmreportwell install
```

If the mailed report says that the Time Machine preferences could not be read,
add `tmreportwell` under System Settings, Privacy & Security, Full Disk
Access.

## Commands

- `tmreportwell report`: Print the report. Sends nothing and changes nothing.
- `tmreportwell run`: Collect new log messages and mail the report if it is due.
- `tmreportwell send`: Collect new log messages and mail the report now.
- `tmreportwell install`: Run `tmreportwell run` every hour as a launch agent.
- `tmreportwell uninstall`: Remove the launch agent.
- `tmreportwell --version`: Print the version.

## Configuration

- `smtp_host`, `smtp_port`, `smtp_user`: Mail server and login. Port 465 uses
  TLS from the start, every other port uses STARTTLS.
- `from`, `to`: Sender and recipient address.
- `send_at`: Time of day for the report, 09:00 by default. If the Mac is asleep
  or the mail server is unreachable then, the report follows at the next hourly
  run that succeeds.
- `warn_days`, `problem_days`: Age of the last completed backup that makes the
  report a WARNING (2) or a PROBLEM (7).

Errors of the launch agent go to `~/Library/Logs/tmreportwell.log`.
