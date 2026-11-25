{{/* DOSSIER.md is the request for a dossier when the conversation is about
  to be restarted. It is the last message of the calls that write it and is
  never recorded; the text of the final reply becomes the body of the dossier
  that opens the next subsession. The model-facing word is "briefing".

  Data:
    .Files    table of the session's record files, oldest first
    .Index    what happened since the last briefing, one line per event,
              each with its file:line
    .Fresh    true when the model no longer has the conversation (the
              provider refused it as too long) and works from the files
    .Reason   why the restart is happening ("" when it is only size) */}}
[eagent] Your conversation has grown long and is about to be restarted from a briefing. Write that briefing now. It is the first thing you will read afterwards and, with the facts listed at the end of this message, all you will know without looking something up.

A briefing is two things: where the session stands, and a MAP of where in the record each thing that matters can be read. The record is never shortened. Later, when the user comes back to something old or the work turns somewhere you did not expect, you will go to the places the map names and read the source, so the map has to cover the whole session, not only the latest stretch.

The record is these files, oldest first. One line of a file is one event, cited as file:line:
{{.Files}}
{{if .Fresh}}
You no longer have the conversation in front of you: the model refused it as too long. Work from the record instead. Read the newest file first with session_read, then follow the map of the briefing that file opens with into the older ones.
{{end}}
What happened since your last briefing, by place in the record:
{{.Index}}

Before you write, look up what you need. session_search finds lines in any file; session_read shows them. The MAP of your present briefing (at the top of your conversation, if there is one) already points into the older files: carry forward every entry that still matters, check the ones you are unsure of, and add entries for what happened since. While you are writing the briefing only session_search and session_read run; no other tool does.

Then write the briefing as your whole reply, in plain text, with no tool call, in these parts:
1. WHAT THE USER WANTS: the goal as it stands now, the standing instructions and preferences they gave (how often to hear from you, what not to ask, how they like things done), and the decisions they made. Use their words where the wording matters.
2. WHERE THE WORK STANDS: what the agent's reports show finished, what it is doing right now, and what failed or is unfinished, with the paths, commands, numbers and commit ids.
3. DECISIONS AND CONSTRAINTS: choices that were made, things that were tried and dropped, and why.
4. OPEN: questions waiting on the user or the agent, anything you were about to do, and any standing order to keep the agent going.
5. WHAT THE USER LAST HEARD: the substance of your last few messages, so that you neither repeat nor contradict them.
6. MAP: one entry per topic that matters (the goal, each piece of the work, each decision, each open problem, anything the user may come back to), each with the places in the record to read, as file:line or file:line-line (for example 1788740857293.jsonl:41-58). Everything in parts 1 to 5 should be traceable through it. Cite only places you were shown: in the list above, in a search or a read, or in your present briefing.

Dense and specific, facts over prose, no preamble: one to three pages.

Added after your text automatically, so leave them out: the table of files; each agent's terminal, folder, model and how to resume it; the user's messages word for word with their places; the agent's turn ends with their places.{{if .Reason}}

Why now: {{.Reason}}.{{end}}
