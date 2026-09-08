# What broke before, and what eagent does about it

eagent is the fifth implementation of the same design document
([`docs/DESIGN-SPEC.md`](DESIGN-SPEC.md)). Four earlier
attempts (written by four different coding agents) were run on the same two
real tasks: build a modern breakout game from a dictated brief, and clone a
Rust repository and build an explainer website for it. None of the four
produced a working result on either task in the final runs. Their session
logs were read line by line before this implementation was designed. This
document lists what went wrong and the specific decision in eagent that
answers each failure. It is the part of the project most worth reading if
you are building an agent harness of your own.

## 1. The session ended the moment the orchestrator stopped calling tools

Two implementations treated any model response without a tool call as "the
orchestrator is idle", and ended the session when nothing else was running.
GLM-5.3 would emit one sentence of narration ("Let me read the file") with no
tool call, and the run was over. One implementation also ended the turn after
a fixed number of model calls (20), which the orchestrator hit while still
reading the repository. In every such case the narrator then sent a cheerful
message ("I've read the brief and I'm building it now") and the process
exited with nothing on disk.

**eagent:** the orchestrator ends a turn only by calling `yield`, and declares
the work finished only with `yield(done=true)`. A tool-less response is
answered with a steering message explaining that nothing happened and asking
for either tools or a yield; only after two such nudges is a yield assumed.
There is no per-turn call cap; a soft steer after `max_orchestrator_calls_per_turn`
consecutive calls (30 by default) suggests delegating or waiting instead. The narrator's final report is triggered by
the yield, not by the process exiting, and it is told whether the session is
actually ending.

## 2. Tool calls leaked as text and were executed as nothing

GLM models occasionally emit their native tool-call markup
(`<tool_call>bash <arg_key>command</arg_key><arg_value>ls</arg_value>`) as
plain text. One implementation rendered its own history with tool calls
flattened to text lines, which taught the model to imitate the format; the
imitation was then recorded as a message, nothing ran, and the session ended
(failure 1). Another implementation executed a tool call whose arguments
failed to parse as `{}`, so a truncated 13KB heredoc became `bash -c ""`,
exit 0, "success".

**eagent:** history is replayed with native structured tool calls and tool
results in every protocol, never flattened. A text response containing
`<tool_call>` markup is parsed as a fallback and executed as a real tool call.
Arguments that are not a JSON object are rejected with an error result that
tells the model what happened; they are never executed as an empty command.

## 3. Task-worker truncation was reported as success

The mid-tier worker spent its entire output budget on reasoning and returned
nothing (`output=16000 reasoning=16000 text=""`), or hit `max_tokens` while
writing a large file so the tool call was dropped by the provider. One
implementation treated `finish_reason=length` as a fatal error that killed
the whole session; another treated the empty response as completion and
reported `success (empty report)` to the orchestrator, four times in one
session. The orchestrator built on reports of files that did not exist.

**eagent:** the worker runs at low reasoning effort by default (on Together,
GLM-5.3-Flash with default effort spent 8,800 reasoning tokens and 69 seconds
on a 120-line file; with `low` it spent 7 tokens and 14 seconds). Output
truncation is detected and answered with a steer to write in smaller pieces.
A worker that stops without calling `complete_task` is reported as failed if
it produced nothing, and its last message is attached either way. A task's
report is one message the orchestrator is told to verify, not to trust.

## 4. One provider hiccup ended the run

A 180-second request timeout, a 503 from Together, a 429 for exhausted OpenAI
credits, and a narrator returning `WAITr` instead of the exact sentinel `WAIT`
each terminated a session in one implementation or another. The credits
failure was retried for two minutes because every 429 was classified as
transient, and there was no fallback route even though the design called for
one.

**eagent:** all calls stream, so a stalled connection is detected by silence
(two minutes) rather than by a hard request timeout; a stream that closes
before the provider signals completion is a transport failure, retried, never
accepted as a fragment. Rate limits and 5xx retry with backoff; exhausted
credits, bad keys, and unknown models are *unroutable* and switch to the
configured fallback route instead. A task-worker failure fails the task, not
the session. An orchestrator call that still fails gets one recovery turn;
after that the session pauses with an honest message and `eagent -c` resumes
it.

## 5. Command output was silently truncated at 4KB

One implementation ran `cmd.Wait()` concurrently with 4KB pipe reads; Go's
`Wait` closes the pipes on exit, so every fast command lost everything after
its first read. `cat README.md` returned 4,055 of 24,614 bytes. The worker
noticed ("stdout seems truncated at 4096 bytes") and adapted by reading files
in `sed -n` slices of forty lines, burning its whole call budget on I/O.

**eagent:** the child's stdout and stderr are connected directly to one pipe
whose write end the parent closes after start; a single pump goroutine reads
until EOF, so nothing is lost, and output up to 4MB per process is retained
(head and tail) with a marker for anything dropped in between. A read of a
whole file is one `read_file` call.

## 6. The narrator was fed a firehose and spoke on a timer

Narrators received every orchestrator event as raw JSON (68,849 tokens of
context after 46 seconds in one run), woke every 30 seconds whether or not
anything had happened, and were forced to speak on wake-ups where they had
nothing to say. The results were hollow ("I'll start by reading INSTRUCTIONS.md
to understand the project, then work through building it. Give me a moment"),
false ("the team is starting to pull down the repo", before any clone), and
promissory ("I'll keep you posted as it takes shape", seven milliseconds
before the process exited).

**eagent:** the narrator sees a compact observation stream: user messages,
the orchestrator's text and tool calls in one line each, tool results clipped
to 1,500 characters, notes, task starts and reports, schedule and process
events. It wakes on notes, task completions, yields, errors, and on a timer
only while work is happening *and* something new is visible. Its prompt bans
promises and offers of follow-ups, requires facts with paths and commands,
and requires a final report when the orchestrator declares the work done. It
is told when the session is about to end so it never says "I'll report back"
to a process that is exiting.

## 7. The orchestrator did the worker's job

With every command costing a poll round-trip and no delegation prompt to
speak of, orchestrators spent their turns on `cat`, `sed`, and `bash_poll`,
and in the one run that produced a working game the orchestrator wrote 30KB
heredocs itself for 145 seconds per call after the worker timed out.

**eagent:** `bash` waits up to twenty seconds inline and returns the output
when the command finished, so a `cat` is one round-trip while the command is
still asynchronous under a handle. The orchestrator's prompt tells it to
delegate substantive work with self-contained task descriptions, run
independent tasks in parallel, block with `wait` instead of polling, and
spend its own calls on orientation and verification.

## 8. Context management was either absent or misfired

One implementation dumped a 626KB `ls` result straight into the orchestrator's
history (245k tokens) because tool results were uncapped and compaction was
checked only between turns. Another dispatched its compaction task before
opening the new subsession file, so the dossier's events landed in the old
file.

**eagent:** every tool result is capped (24k characters, head and tail). The
orchestrator's prompt size is read from provider usage after every call; when
it passes the threshold the current file is closed, the next one is opened,
and *then* the dossier task is created, so its events live in the new
subsession. The dossier must cite `file:line` locations in the logs and is
rejected and retried if it does not.

## 9. Small things that were not small

- **Cloudflare 1010.** Together AI rejects requests whose `User-Agent` is a
  library default. eagent sends a real one.
- **Zombie supervisors and `defunct` processes** from `Process.Release()`
  without `Wait`. eagent waits on every child.
- **A stale session lock** prevented resuming after a crash in one
  implementation; another refused to reopen any session that had ended.
  eagent uses an advisory `flock` (released by the kernel when the process
  dies) and can resume any session.
- **Writes outside the project.** A worker wrote a zero-byte file to a
  mistyped absolute path outside the project and reported it created. eagent
  confines writes to the project directory (reads may go anywhere).
- **Prompt cache misses on OpenAI** because the growing prefix changed each
  call. eagent renders history deterministically from the log, keeps system
  prompts and tool lists fixed per subsession, and appends all per-call
  steering as the final message. Measured on a 55-call orchestrator session:
  93% of input tokens served from cache.

## 10. What a six-preset grid taught

On 2026-09-07 six presets each built the same game spec in parallel, and a
seventh session measured the results: cost, tokens, cache ratio, rubric
scores against the spec, and scripted Chromium play. Profiling the six event
logs found four more habits worth fixing, and the fixes are in the history:
steers are recorded as events so each prompt is an exact extension of the
last, restoring OpenAI's prefix cache (`fb9bd9d`); every actor has a route
fallback and a billing failure is never retried (`95b364a`); unchanged
re-reads and re-viewed screenshots are curbed, and an orchestrator doing the
workers' job is told to delegate (`d08c0d4`); and a gateway 404 "no endpoints
support image input" is treated as an image rejection and falls back to text
(`4b8e277`). The exercise also left an open question: a playable game
existed after 15 minutes and the run spent another 2 hours 50 minutes on
polish passes because the prompt said "relentlessly" and nothing in the
harness signals diminishing returns. The full grid, with costs and evidence,
is [docs/breakout-grid.md](breakout-grid.md).
