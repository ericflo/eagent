{{/* USER_ANSWER.md renders one user.answer event for the orchestrator.

  Data:
    .Text        the answer text
    .Time        the event time (time.Time, UTC as persisted by store.Append)
    .Timestamp   preformatted local time "2006-01-02 15:04:05" from .Time; use
                 this, never a "now" function, so rendering is deterministic
                 and replay-stable (prefix-cache safe)
    .Source      where the answer came from: "" (terminal), "web", "finalechat"
    .Nick        IRC nick derived from .Source: "user", "user@web", "user@phone",
                 or "user@<source>" for anything else
    .Attachments pre-rendered attachment notes ("" when none)
    .QuestionID  the answered narrator question's id
    .Question    the question restatement ("text (options: a | b)" or "" when
                 the question was asked in this subsession and needs no restating)

  Keep the leading line in "[ts] <nick> text" chatlog form so turns stay
  byte-identical across replays and prompt-cache prefixes keep hitting. */}}
[{{.Timestamp}}] <{{.Nick}}> {{.Text}}{{.Attachments}}{{if .Question}}
[The user answered question {{.QuestionID}}, {{printf "%q" .Question}}]{{else}}
[The user answered question {{.QuestionID}}]{{end}}
