{{/* DOSSIER.md is the request for a briefing when the conversation is about
  to be restarted. It is sent as the last message of one model call and is
  never recorded; the reply's text becomes the body of the dossier that opens
  the next subsession.

  Data:
    .Reason   why the restart is happening ("" when it is only size) */}}
[eagent] Your conversation has grown long and is about to be restarted from a briefing. Write that briefing now, as your whole reply, in plain text and with no tool call. It is the first thing you will read afterwards and, apart from the facts listed next, the only thing you will know.

Added for you automatically, so leave them out: each agent's terminal, folder, model and how to resume it; every message the user sent, word for word, with its place in the record; a list of the agent's turn ends with their places in the record.

Write these four parts, in this order:
1. WHAT THE USER WANTS: the goal as it stands now, the standing instructions and preferences they gave (how often to hear from you, what not to ask, how they like things done), and the decisions they made. Use their words where the wording matters.
2. WHERE THE WORK STANDS: what the agent's reports show finished, what it is doing right now, and what failed or is unfinished, with the paths, commands, numbers and commit ids.
3. OPEN: questions waiting on the user or the agent, anything you were about to do, and any standing order to keep the agent going.
4. WHAT THE USER LAST HEARD: the substance of your last few messages, so that you neither repeat nor contradict them.

Dense and specific, one page at most, no preamble.{{if .Reason}}

Why now: {{.Reason}}.{{end}}
