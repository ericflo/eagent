{{/* DOSSIER.md is the request for a dossier when the conversation is about
  to be restarted. It is the last message of the calls that write it and is
  never recorded; the text of the final reply becomes the body of the dossier
  that opens the next subsession. The model-facing word is "briefing".

  Data:
    .Files    table of the session's record files, oldest first
    .Index    the record by place (file:line): everything before the last
              briefing in outline, what happened since line by line
    .Fresh    true when the model no longer has the conversation (the
              provider refused it as too long, or there never was one)
              and works from the files
    .Legacy   true when the record was written by the earlier eagent that
              did the work itself
    .Reason   why the restart is happening ("" when it is only size) */}}
{{if .Legacy}}[eagent] This session was started by an earlier eagent, which did the work itself with helpers of its own (its record shows tasks, notes and tool calls of a kind you do not have). That way of working is gone: a coding agent in a terminal does the work now and you stand between it and the user. You are picking the session up from its record. Write the briefing you will continue from. It is the first thing you will read afterwards and, with the facts listed at the end of this message, all you will know without looking something up.{{else}}[eagent] Your conversation has grown long and is about to be restarted from a briefing. Write that briefing now. It is the first thing you will read afterwards and, with the facts listed at the end of this message, all you will know without looking something up.{{end}}

A briefing is two things: where the session stands, and a MAP of where in the record each thing that matters can be read. The record is never shortened. Later, when the user comes back to something old or the work turns somewhere you did not expect, you will go to the places the map names and read the source, so the map has to cover the whole session, not only the latest stretch.

The record is these files, oldest first. One line of a file is one event, cited as file:line:
{{.Files}}
{{if .Legacy}}
You have no conversation to work from, only the record. Read the newest file first with session_read, then the older ones; where a file opens with a briefing the earlier eagent wrote, read it and follow its map.
{{else if .Fresh}}
You no longer have the conversation in front of you: the model refused it as too long. Work from the record instead. Read the newest file first with session_read, then follow the map of the briefing that file opens with into the older ones.
{{end}}
The record by place:
{{.Index}}

Study the record before you write. session_search finds lines in any file; session_read shows them. Build the briefing from the record every time, the whole of it, not from the briefing before: earlier briefings (yours at the top of your conversation, and the one each file opens with) are sources like any other, good for finding where things are and no better than the lines they cite. Read the places that matter again, in the old files as well as the new, and keep an entry only when the record still bears it out. A topic that has gone quiet stays on the map: the user may come back to it. While you are writing the briefing only session_search and session_read run; no other tool does.

Then write the briefing as your whole reply, in plain text, with no tool call, in these parts:
1. WHAT THE USER WANTS: the goal as it stands now; every instruction the user gave the agent, in their words; the standing instructions and preferences they gave you (how often to hear from you, what not to ask, how they like things done); and the decisions they made.
2. WHERE THE WORK STANDS: for each of those instructions, whether the agent's turn reports show it done, in progress, or not acted on yet; what the agent is doing right now; and what failed or is unfinished. Give the paths, commands, numbers, commit ids and error messages.
3. DECISIONS AND CONSTRAINTS: choices that were made, things that were tried and dropped, and why.
4. OPEN: questions waiting on the user or the agent, anything you were about to do, and any standing order to keep the agent going.
5. WHAT THE USER LAST HEARD: the substance of your last few messages, so that you neither repeat nor contradict them.
6. MAP: one entry per topic that matters (the goal, each piece of the work, each decision, each open problem, anything the user may come back to), each with the places in the record to read, as FILE:LINE or FILE:LINE-LINE, with the file's full name from the table above. Everything in parts 1 to 5 should be traceable through it. Cite only places you were shown: in the lists above, in a search or a read, or in an earlier briefing.

Dense and specific, facts over prose, no preamble: one to three pages.

Added after your text automatically, so leave them out: the table of files; each agent's terminal, folder, model and how to resume it; the user's messages with their places (the first few and the latest word for word, the ones between in a line each); the agent's turn ends with their places.{{if .Reason}}

Why now: {{.Reason}}.{{end}}
