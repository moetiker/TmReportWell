# Maintainer notes

Observed on macOS 27.2.

- Completed backups come from `SnapshotDates` in
  `/Library/Preferences/com.apple.TimeMachine.plist`. `tmutil latestbackup` and
  `tmutil listbackups` fail while the backup disk is unplugged.
- The `com.apple.TimeMachine` log messages reach back only about a day, although
  the log archive itself covered 15 days. The agent therefore collects hourly
  and keeps the grouped messages in `state.json` until a report is mailed.
- Log messages are matched only to explain skipped backups and to drop noise.
  No backup ran while the patterns were written, so the messages of a running
  backup are unknown. Anything unrecognised is shown verbatim.
- The last line of `log show --style ndjson` is a trailer without a timestamp.
