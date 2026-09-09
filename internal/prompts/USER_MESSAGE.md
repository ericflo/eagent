{{/* USER_MESSAGE.md renders one user.message event for the orchestrator.

  Data:
    .Text        the message text
    .Time        the event time (time.Time, UTC as persisted by store.Append)
    .Timestamp   preformatted local time "2006-01-02 15:04:05" from .Time; use
                 this, never a "now" function, so rendering is deterministic
                 and replay-stable (prefix-cache safe)
    .Source      where the message came from: "" (terminal), "web", "finalechat"
    .Nick        IRC nick derived from .Source: "user", "user@web", "user@phone",
                 or "user@<source>" for anything else
    .Attachments pre-rendered attachment notes ("" when none)
    .Client      the client capsule's source ("" when the client sent none)
    .ClientLine  the client capsule in one line, e.g. "web · tz America/Los_Angeles · en-US"
                 ("" when the client sent none; the bracket is omitted then so
                 lines without caps render byte-identical to before)

  Keep the leading line in "[ts] <nick> text" chatlog form so turns stay
  byte-identical across replays and prompt-cache prefixes keep hitting. */}}
[{{.Timestamp}}] <{{.Nick}}> {{.Text}}{{.Attachments}}{{if .ClientLine}} [{{.ClientLine}}]{{end}}
