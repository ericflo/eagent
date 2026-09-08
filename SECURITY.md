# Security

eagent runs shell commands on your machine as you and holds API keys in its
environment, so problems that let a model or a session log do more than
that deserve a quiet report.

## Reporting a vulnerability

Please do not open a public issue for a security problem. Use GitHub's
private vulnerability reporting on this repository ("Report a vulnerability"
under the Security tab). Include what you found, how to reproduce it (a
session log under `.agents/eagent/sessions/` is ideal, with any keys
removed), and what you think the impact is. You will get an acknowledgement
within a few days.

## What counts

- A way for model output, a tool result, an inbox file, a Finalechat message
  or a downloaded archive to write outside the project directory, run a
  command the harness did not intend, or leak credentials.
- The web UI (`eagent serve`) accepting requests it should not, or exposing
  keys it should not.
- Session archives or settings connectors carrying more than they document.

## What does not

eagent is not a sandbox: the README says so, and commands run with your
privileges by design. A prompt that convinces the model to do something
unwise is a prompt problem, not a vulnerability, unless the harness's own
guardrails (write confinement, the kill switch, the connector validation)
fail to hold.
