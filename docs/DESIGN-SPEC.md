# eagent: Design Specification

`eagent` is an agent harness and agent runner, delivered as a single-file binary. It runs three cooperating LLM actors over an append-only, event-sourced session log that lives in the working directory alongside the code it operates on.

This document describes the intended design: the session model, the three actors and their responsibilities, how context limits are handled, how tools execute, and which model providers back each actor.

## 1. Goals

- **Single binary.** The implementation language is unconstrained. Go, Rust, or anything else is acceptable as long as the result is one self-contained executable file.
- **Event sourced.** Everything that happens in a session is appended to a JSONL log. Replaying that log reconstructs the full state of the session, so a crash or interruption never loses work.
- **Three independent actors.** Rather than one main agent that occasionally spawns subagents, eagent runs three actors by default, each with its own job, its own context model, and its own model tier: an orchestrator, a task worker, and a narrator.
- **Prefix caching as a first-class concern.** The actors that carry continuous history are structured so their prompts stay cache-friendly across turns.
- **No lossy compaction.** When context runs out, a fresh subsession begins with a purpose-built dossier that points back into the full-fidelity history rather than replacing it.
- **Asynchronous tool execution.** Every shell command runs in the background under a handle from the start, so the harness always stays in control.
- **Local, checked-in storage.** Sessions are stored in the project directory, not in a home directory or a database.

## 2. Sessions and Event Sourcing

### 2.1 The session log

A session is a JSONL file. Every event produced by any actor is appended as a line: orchestrator turns, task hand-offs and their results, and everything else the actors do. The log is append-only and is the single source of truth. If the process crashes, restarting and replaying every line returns the system to exactly where it was.

### 2.2 One log, many readers

All three actors write into the same unified session log, but not every actor needs every event. During replay, each actor rebuilds its state from the subset of events relevant to it. The orchestrator does not need to read the task worker's internal events, and the task worker does not need to read the orchestrator's. Filtering happens at replay time; the log itself stays complete.

### 2.3 Storage layout

When the binary is invoked, it looks for a `.agents` directory in the current working directory, with the following structure inside it:

```
.agents/
└── eagent/
    └── sessions/
        └── <session>/
            ├── <epoch-timestamp>.jsonl
            ├── <epoch-timestamp>.jsonl
            └── ...
```

- Each directory under `sessions/` is one logical session.
- Each file inside a session directory is a **subsession**: one JSONL log covering a single stretch of the orchestrator's context window (see section 5).
- Subsession files are named with epoch timestamps, so sorting the filenames lexicographically orders them chronologically. Replay iterates through the files in that order to walk forward in time.

Sessions live in the project directory deliberately. Storing them next to the code, rather than in a per-user home directory or a SQLite database, makes it natural to check the entire session history into the repository along with the work it produced.

Because the layout is just timestamped files, session directories from different checkouts or different people can be pooled together. Timestamps from independently running machines will not interleave perfectly, and this is accepted as a known limitation: the ordering is good enough to iterate through, and it is the dossier process in section 5, not the file ordering, that is responsible for making sense of the combined history.

## 3. The Three Actors

Most agent systems have one main agent that does everything, perhaps with subagents. eagent instead separates three concerns into three independent actors. The orchestrator and the task worker are each fully occupied by their own jobs: the orchestrator with figuring out what belongs together, how to break work into smaller steps, how to sequence those steps, and how to stay on track; the task worker with completing tasks well and quickly. Neither has attention to spare for how results should be phrased and presented to a person. The narrator exists to take that job off both of them.

### 3.1 Orchestrator

The orchestrator is the main agent and the most important of the three. It decides what happens in the session.

**Context model.** The orchestrator has continuity. It maintains a continuous history in which every turn it produces is appended, so each new turn has full knowledge of everything before it. This is a conventional agent setup.

**Delegation.** When the orchestrator encounters a task that it does not want to do itself, that will involve a lot of small context-gathering steps, or that is simply easy enough that it should not spend its own context on it, it writes a standalone task description and hands it to the task worker. The task worker's result comes back to the orchestrator through the session log.

**Notes for the narrator.** The orchestrator has a hint tool for jotting down notes it thinks the narrator would want to relay to the user. Each note wakes the narrator (see section 3.3).

**Loops and scheduled work.** The orchestrator can set up loops and background cron-style jobs that wake it later. A session does not end while any loop is active or any scheduled job is pending. This lets the orchestrator decide that a piece of work is ongoing and keep the session alive for it.

**Model tier.** The orchestrator uses the largest and most capable model of the three.

### 3.2 Task Worker

The task worker exists to do the work the orchestrator delegates to it.

**Context model.** The task worker has no continuous history. Each task it receives starts a brand-new context window. Within that window it runs a normal agentic loop, using its tools until the task is done, then reports its result back in a form that both the orchestrator and the narrator can read. The next task starts from a fresh context again.

**Scope.** The task worker cannot talk to the user and cannot do anything other than carry out the task it was given.

**Priorities.** The task worker is optimized for task completion rate, quality of work, and speed.

**Model tier.** The task worker uses a mid-tier model: capable enough to get real work done, cheap and fast enough to be used freely, but not required to be the best, the cheapest, or the fastest of the three.

### 3.3 Narrator

The narrator is the only actor allowed to communicate with the user.

**What it sees.** The narrator receives essentially the same history the orchestrator sees, with two substitutions: its own system message, and its own final user message that elicits its response. Both of these reiterate how the narrator should voice things, what persona to use, how to phrase its output, and how to package everything up for the user.

**When it wakes.** The narrator wakes either periodically or whenever the orchestrator jots down a note. On waking, it reviews its history and what the orchestrator has done and makes a judgment call: is there enough here to send the user a message now, or is it better to wait for some state to be reached first? This is deliberately a judgment rather than a rule. Making the narrator a real LLM agent means these common-sense decisions can be expressed in prompts instead of rigid logic.

**Tools.** The narrator has two tools:

1. **Send message to user.** Its primary tool.
2. **Ask user a question.** Analogous to Claude Code's ask-user-question tool, for when the narrator needs input from the user rather than just delivering output.

**Model tier.** The narrator uses the fastest and cheapest model of the three.

### 3.4 Model assignment

| Actor | Tier | Model | Provider |
|---|---|---|---|
| Orchestrator | Largest, most capable | GPT-6 Astra | OpenAI (OpenRouter as fallback) |
| Task Worker | Mid-tier | `zai-org/GLM-5.3-Flash` | Together AI |
| Narrator | Fastest, cheapest | `deepseek-ai/DeepSeek-V4-Flash-0731` | Together AI |

See section 7 for provider and credential details.

## 4. Prefix Caching

The orchestrator and the narrator both carry continuous history, so their prompts grow with every turn. Prefix caching has to be treated as a first-class design constraint for both.

- **Never change the beginning of the prompt between turns.** The system message must not be swapped out or edited from one turn to the next; doing so invalidates the cached prefix.
- **Steer from the end.** Any steering the harness wants to apply goes at the end of the conversation: as a new system-style message delivered in the user role, or as extra steering content injected into the final user message that elicits the response. The changing part of the prompt is always the last message.
- **Use the provider's cache controls.** As new messages arrive, mark content as cacheable or ephemeral according to what each upstream API supports, so that the stable prefix is actually reused.

## 5. Context Limits: Dossiers Instead of Compaction

eagent does not use conventional compaction, where the tail of a conversation is summarized and the summary replaces the history. It uses a different mechanism built on subsessions and the task worker.

### 5.1 Subsessions

The orchestrator is the actor that will fill its context window first, because it is the one holding continuous history. When the orchestrator hits its limit:

1. The current subsession ends and a new subsession file begins in the same session directory.
2. The orchestrator pauses.
3. The task worker is given a task, along with the directory containing all of the session's subsessions.

One logical session is therefore made up of many subsessions, each ending when the orchestrator's context filled.

### 5.2 The dossier task

The task worker's assignment is to produce a brief, or dossier, of everything in the previous subsessions that is relevant to the orchestrator's current work: what it was just doing, what it just finished, and what it is working toward.

The dossier is explicitly not just a summary. It must also be a **map into the session logs**: pointers to exactly where in the subsession JSONL files the orchestrator can go to read the full context around any item. When the orchestrator starts its new subsession with the dossier in hand, it should have most of what it needs, and for anything more it can follow the map and read the original, full-fidelity events itself.

Because the task worker is a full agent with tools, it does not have to work from a fixed input. It can explore all of the subsessions and decide for itself what is most relevant to the work going forward.

### 5.3 Why this is better than compaction

- **Compaction happens at the start of a subsession, never at the end.** Nothing is thrown away when a context window fills; the new subsession simply opens with a dossier.
- **Compaction is over everything, not just the last subsession.** Traditional compaction feeds each new context only the compacted output of the previous one. Over many rounds, material from the oldest sessions erodes away, even though it may be the most important material there is. The dossier is rebuilt from all subsessions every time, so nothing is ever a copy of a copy of a copy.
- **Each dossier is built just in time for the work at hand.** One compaction can zoom in on what matters right now; the next can zoom in on something else.
- **Two levels of fidelity.** The dossier is the first level. The full session logs, reachable through the map, are the second. The orchestrator is never limited to the summary.
- **Every subsession is self-describing.** As a side effect, every subsession after the first opens with a dossier from the task worker, so anyone reading a subsession file back later starts with context.

### 5.4 Scope

This mechanism is designed for the orchestrator. The narrator and the task worker are not expected to exhaust their context windows under normal use; if they do, that will be addressed when it comes up.

## 6. Tool Execution: Asynchronous Bash

### 6.1 The tool

Actors do real work on the system through a single tool: bash. Exposing bash is the simplest way to give an agent broad capability, since from bash it can run inline Python, inline JavaScript, or plain shell scripting as the job requires.

For now, commands run directly on the same machine where the agent's filesystem lives. There is no sandboxing (see section 8).

### 6.2 Every command is asynchronous

A naive blocking bash tool fails in predictable ways. A command waits on standard input and never returns. A script enters an infinite loop. A query joins the wrong tables and takes forever. In every case the agent is stuck.

The usual response is to start from a blocking model and bolt on safeguards: cancel after some timeout, grant another sixty seconds, keep extending. eagent starts from the other end instead. **Every bash command runs asynchronously in the background and immediately returns a handle**, an identifier the harness uses to track it.

### 6.3 Harness responsibilities

With every command backgrounded, managing running commands becomes the harness's job. For each live handle it must be able to decide, asynchronously:

- whether the command has run too long and should be cancelled;
- whether the command is waiting for input and something should be sent to it over a pipe;
- whether the command's timeout should be extended.

Starting from this model makes the whole system easier to reason about as it grows.

## 7. Providers and Credentials

### 7.1 Supported APIs

eagent supports two inference APIs:

- the **OpenAI Responses API**, and
- the **Anthropic Messages API**.

### 7.2 Credentials

The following API keys are expected in the environment:

| Variable | Purpose |
|---|---|
| `OPENAI_API_KEY` | Orchestrator, via OpenAI directly |
| `TOGETHER_API_KEY` | Task worker and narrator, via Together AI's inference platform |
| `OPENROUTER_API_KEY` | Fallback for the orchestrator |

### 7.3 Model routing

- **Orchestrator:** GPT-6 Astra through the OpenAI API key. If that key does not have access to Astra, use the OpenRouter key and OpenRouter's Astra support via `openai/gpt-6-astra`.
- **Task worker:** `zai-org/GLM-5.3-Flash` on Together AI.
- **Narrator:** `deepseek-ai/DeepSeek-V4-Flash-0731` on Together AI.

## 8. Out of Scope

The following are consciously deferred:

- **Sandboxing and remote execution.** There are ideas about where execution should happen, including isolated sandboxes and remote clusters. For now, everything runs on the local machine against the local filesystem.
- **Context exhaustion in the narrator or task worker.** Handled if and when it occurs; see section 5.4.
