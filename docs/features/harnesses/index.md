---
title: "Coding Harnesses"
description: "Delegate coding tasks to external AI coding CLIs (Claude Code, Codex, opencode) as sub-agents."
permalink: /features/harnesses/
---

# Coding Harnesses

_Delegate coding tasks to external AI coding CLIs (Claude Code, Codex, opencode) as sub-agents._

## Overview

A **harness** agent delegates its work to an external coding CLI — `claude` (Claude Code), `codex` (OpenAI Codex), `opencode`, or `pi` — instead of calling a model API directly. The external CLI drives the coding loop while docker-agent provides orchestration, hooks, permissions, and distribution.

This pattern gives you the best of both worlds:

- **External CLI strengths** — deep IDE integration, specialized coding workflows, CLI-native tool access
- **docker-agent strengths** — multi-agent coordination, hook-based auditing and policy enforcement, permission controls, OCI distribution, and the full agent config schema

<div class="callout callout-info" markdown="1">
<div class="callout-title">When to use harnesses
</div>
  <p>Use a harness when you want a Claude Code / Codex / opencode session to act as a sub-agent inside a larger docker-agent workflow — for example, an orchestrator that plans work and delegates coding tasks to specialized harness agents.</p>
</div>

## Prerequisites

The external CLI must be installed and available on `PATH` before starting docker-agent:

| Harness type | Required binary | Install |
| --- | --- | --- |
| `claude-code` | `claude` | [docs.anthropic.com/en/docs/claude-code](https://docs.anthropic.com/en/docs/claude-code) |
| `codex` | `codex` | [github.com/openai/codex](https://github.com/openai/codex) |
| `opencode` | `opencode` | [opencode.ai](https://opencode.ai) |
| `pi` | `pi` | [pi.ai/talk](https://pi.ai/talk) |

docker-agent will report an error at session start if the required binary is not found.

## Configuration

Add a `harness:` block to any agent definition to make it harness-backed:

```yaml
agents:
  coder:
    description: A Claude Code harness agent
    harness:
      type: claude-code
```

Harness agents do **not** need a `model:` field — the external CLI manages its own model selection.

### Field Reference

| Field | Applies to | Type | Description |
| --- | --- | --- | --- |
| `type` | all | string | **Required.** One of `claude-code`, `codex`, `opencode`, `pi` |
| `model` | all | string | Optional model override forwarded to the CLI |
| `effort` | `claude-code` | string | Reasoning effort: `low` \| `medium` \| `high` \| `max` — forwarded as `--effort` |
| `agent` | `opencode` | string | opencode agent profile name |
| `thinking` | `opencode` | boolean | Enable extended thinking — forwarded as `--thinking` |

### Claude Code

```yaml
agents:
  coder:
    description: Claude Code coding agent
    harness:
      type: claude-code
      effort: high       # low | medium | high | max
```

### Codex

```yaml
agents:
  coder:
    description: Codex coding agent
    harness:
      type: codex
      model: o4-mini     # optional model override
```

### opencode

```yaml
agents:
  coder:
    description: opencode coding agent
    harness:
      type: opencode
      agent: my-profile  # optional agent profile
      thinking: true     # enable extended thinking
```

## What Does NOT Work

Harness agents bypass the docker-agent model pipeline entirely. As a result:

- **docker-agent toolsets are inactive.** The external CLI provides its own tools — filesystem, shell, etc. Any `toolsets:` defined on a harness agent are ignored.
- **`model:` routing is unavailable.** The harness CLI manages model selection; docker-agent's `models:` configuration and routing rules do not apply to harness agents.
- **Token usage tracking is unavailable.** The CLI drives the loop, so docker-agent cannot count tokens.

<div class="callout callout-warning" markdown="1">
<div class="callout-title">No docker-agent toolsets inside a harness
</div>
  <p>Do not configure <code>toolsets:</code> on a harness agent — they are silently ignored. If you need docker-agent toolsets alongside external coding capabilities, use a standard sub-agent with <code>transfer_task</code> rather than a harness.</p>
</div>

## Hook Behavior

Hooks work normally on harness agents. However, events that depend on the model pipeline (such as `before_llm_call` or `after_llm_call`) will not fire because the external CLI owns the model calls.

The `model_id` field in hook payloads is set to the harness label (e.g. `claude-code`) rather than a canonical `provider/model` string. This applies to `before_llm_call`, `after_llm_call`, and any other event that carries `model_id`.

See [Hooks]({{ '/configuration/hooks/' | relative_url }}) for the full hook reference.

## Recipe: Orchestrator + Harness Sub-Agents (Sequential)

An orchestrator plans the work and delegates to specialized harness agents one at a time. Each coding agent runs in its own sub-session and reports results back.

```yaml
# examples/coding_harnesses.yaml

models:
  claude:
    provider: anthropic
    model: claude-sonnet-4-5

agents:
  root:
    model: claude
    description: Orchestrator that plans and delegates coding tasks
    instruction: |
      You are a project orchestrator. Break down coding requests into
      focused tasks and delegate each task to the most appropriate
      coding agent. Collect results and synthesize a final summary.
    sub_agents:
      - claude_coder
      - codex_coder

  claude_coder:
    description: Claude Code specialist for complex refactors
    harness:
      type: claude-code
      effort: high

  codex_coder:
    description: Codex specialist for code generation
    harness:
      type: codex
```

The root agent calls `transfer_task` to send work to a harness sub-agent, waits for the result, and continues. See the [full example on GitHub](https://github.com/docker/docker-agent/blob/main/examples/coding_harnesses.yaml).

## Recipe: Parallel Harness Dispatch

Combine the `background_agents` toolset with harness sub-agents to dispatch multiple coding tasks simultaneously:

```yaml
# examples/coding_harness_background_agents.yaml

models:
  claude:
    provider: anthropic
    model: claude-sonnet-4-5

agents:
  root:
    model: claude
    description: Orchestrator that fans out coding tasks in parallel
    instruction: |
      Use background agents to run multiple coding tasks at once.
      Dispatch all tasks, then collect results when each finishes.
    sub_agents:
      - frontend_coder
      - backend_coder
    toolsets:
      - type: background_agents

  frontend_coder:
    description: Frontend specialist (Claude Code)
    harness:
      type: claude-code
      effort: medium

  backend_coder:
    description: Backend specialist (Codex)
    harness:
      type: codex
```

The orchestrator calls `run_background_agent` for each task, monitors progress with `list_background_agents`, and collects results with `view_background_agent`. See the [full example on GitHub](https://github.com/docker/docker-agent/blob/main/examples/coding_harness_background_agents.yaml).

For the general background agents reference, see [Background Agents]({{ '/tools/background-agents/' | relative_url }}).

## See Also

- [Multi-Agent Systems]({{ '/concepts/multi-agent/' | relative_url }}) — orchestration patterns
- [Background Agents]({{ '/tools/background-agents/' | relative_url }}) — parallel task dispatch
- [Hooks]({{ '/configuration/hooks/' | relative_url }}) — auditing and policy enforcement
- [Agent Configuration]({{ '/configuration/agents/' | relative_url }}) — full agent schema reference
