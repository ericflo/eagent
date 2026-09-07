Write a dossier for the orchestrator, whose context window just filled up. It is about to start a fresh context and this dossier is the first thing it will read. Your dossier must let it continue the work without losing anything important, and must map where in the session logs to look for full detail.

Session directory: {{.SessionDir}}
Subsession files (oldest first): {{.Files}}

Use session_list, session_read, and session_search to study the logs. Start with session_read on the newest file (the most recent work), then the earlier ones. Read the user's messages verbatim. Follow task reports and tool results to establish what is actually on disk; check the filesystem with bash or read_file when it matters.

Write the dossier in this shape (markdown):
1. USER'S GOAL: the request(s) as the user wrote them, plus any clarifications or decisions they made.
2. CURRENT STATE: what exists now (paths), what is verified working, what is in progress (running tasks/processes and their handles), what is broken or unfinished.
3. DECISIONS AND CONSTRAINTS: architecture choices, conventions, things that were tried and rejected, and why.
4. NEXT STEPS: the concrete work remaining, in order, as the orchestrator was about to do it.
5. MAP: for each important topic (goal, each major component, each open problem, each decision), the exact places to read in the logs as file:line or file:line-line citations (e.g. 1788740857293.jsonl:41-58). Every section above should be traceable through this map.

Be dense and specific: paths, commands, handles, error messages. Prefer facts over prose. Do not pad. Aim for the amount of detail a careful engineer would want when taking over a colleague's half-finished work: usually 1-3 pages.

When finished, call complete_task with status "completed" and the entire dossier as the summary.
{{if .Reason}}
Reason for the reset: {{.Reason}}.
{{end}}{{if .RunningTasks}}
Tasks currently running (their reports will arrive after your dossier):
{{range .RunningTasks}}- {{.}}
{{end}}{{end}}
