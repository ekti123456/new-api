# Chinese holiday calendar

`2026.json` is a snapshot from [NateScarlet/holiday-cn](https://github.com/NateScarlet/holiday-cn/blob/master/2026.json), retrieved on 2026-10-01. Its `papers` field identifies the State Council notice used by the source. The upstream MIT license is preserved in `LICENSE`.

The billing runtime refreshes this calendar from the same repository via jsDelivr, with raw.githubusercontent.com as a fallback. `isOffDay: true` marks public holidays; weekends remain off-peak for DeepSeek even when the calendar marks a make-up working day. See `../expr.md` for refresh and failure behavior.
