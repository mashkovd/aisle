# aisle — cross-agent session navigator

**aisle discovers the AI coding sessions you already have — whichever agent created them — and lets you find and resume them from one place.**

You have months of conversations spread across Claude Code, Codex, Gemini CLI and Antigravity, each hidden in its own storage format and its own `--resume` flag. aisle reads all of them, shows one list newest-first, and drops you back into the right conversation, in the right directory, inside tmux.

![aisle: list sessions from four agents, full-text search a word that only appears in a reply, resume that conversation in tmux](demo/aisle.gif)

aisle is a thin layer, not an orchestrator: it **discovers → normalizes → shows → hands over to the agent's own CLI**. It never runs or drives an agent itself.

## Install

```sh
brew install --cask mashkovd/tap/aisle
```

or, with Go 1.25+:

```sh
go install github.com/mashkovd/aisle/cmd/aisle@latest
```

tmux is required to resume sessions (the cask installs it).

## Use

```sh
aisle                 # interactive navigator
aisle list            # table of every session, newest first
aisle list --json     # the same, machine-readable
aisle search vault token   # full-text search across every agent's conversations
aisle resume 0d71ed   # resume by ID prefix (or engine:prefix)
aisle new claude .    # start a fresh session in tmux
aisle new codex --worktree   # …in its own git worktree, so parallel agents don't collide
aisle doctor          # agents found, storage paths, formats, warnings
aisle rules check     # does this project's AGENTS.md reach every agent?
```

In the navigator: `enter` resumes, `/` filters titles and projects, `f` searches the full text of your conversations, `n` starts a new session (`w` in the picker: in a new git worktree), `p` groups by project, `?` shows all keys. Shortcuts like `c3` or `x1` are shown for orientation; on the command line always use the stable session ID.

Resuming a conversation that aisle already opened in tmux attaches to that tmux session instead of starting the agent twice.

## Supported agents

| Agent | Reads | Resumes with |
|---|---|---|
| Claude Code | `~/.claude/projects/*/*.jsonl`, `~/.claude/history.jsonl` | `claude --resume <id>` |
| Codex CLI | `~/.codex/sessions/**/rollout-*.jsonl`, `~/.codex/session_index.jsonl` | `codex resume <id>` |
| Gemini CLI | `~/.gemini/tmp/*/chats/session-*.jsonl` (and legacy `.json`) | `gemini --resume <id>` |
| Antigravity CLI | `~/.gemini/antigravity-cli/conversation_summaries.db` (read-only) | `agy --conversation <id>` |

These are the agents' internal formats and they change between releases. aisle either reads a format correctly or reports it: `aisle doctor` lists every file it could not read and every conversation it could not resume. It never drops history silently.

## Full-text search

`aisle search <words…>` (or `f` in the navigator) searches what you and the agents actually wrote — prompts and replies, not tool output or hidden reasoning — across all agents at once. Every word must match, case-insensitively and in any language; the last word also matches as a prefix. Results are grouped per conversation with the best-matching snippet, and `enter` resumes it.

| Agent | What is searchable |
|---|---|
| Claude Code, Codex CLI, Gemini CLI | prompts and replies |
| Antigravity CLI | prompts and titles (replies are stored in an undocumented binary format) |

Search uses a local SQLite FTS5 index. The first search builds it (a few seconds for gigabytes of history); later searches only read what changed. The index lives in your user cache directory (`aisle doctor` shows the path), is readable only by you (0600), and **contains copies of your prompts** — anything you pasted into an agent, including secrets, is in it. It is only a cache: `aisle index --purge` deletes it, and the next search rebuilds it.

## One AGENTS.md for every agent

Each agent looks for its instructions in its own place and follows its own include syntax, so an `AGENTS.md` that one agent reads can be silently invisible to another. `aisle rules` shows what each agent actually loads and fixes the gaps.

```sh
aisle rules check            # which files each agent loads; exit 1 on problems (use it in CI)
aisle rules sync             # print the edits that would fix them
aisle rules sync --apply     # make them; every edited file is backed up to <file>.aisle-bak
aisle rules init             # create AGENTS.md (or --from CLAUDE.md to rename yours) and point every agent at it
aisle rules check --global   # your user-level files instead of a project
```

What it checks:

| Agent | Reads AGENTS.md | Includes | Catches |
|---|---|---|---|
| Claude Code | by itself from 2.1.277, but only when there is no `CLAUDE.md`, `.claude/CLAUDE.md` or `CLAUDE.local.md` | `@path`, relative to the including file | a `CLAUDE.md` that hides AGENTS.md; `@AGENTS.md` in `.claude/CLAUDE.md`, which points to `.claude/AGENTS.md` |
| Codex CLI | by itself | none | `AGENTS.override.md` shadowing it; files over `project_doc_max_bytes` (32 KiB) |
| Gemini CLI | when `context.fileName` lists it | `@path` | the old top-level `contextFileName` key, which current Gemini ignores; AGENTS.md loaded twice |
| Antigravity | by itself, next to `GEMINI.md` | `@[label](path)` only | a `GEMINI.md` that repeats AGENTS.md; files over 24 000 bytes |

`@path` includes inside AGENTS.md are flagged too: Codex and Antigravity read them as plain text.

Edits are conservative: sync adds an include line, rewrites one broken include, moves a settings key, or creates a file that does not exist. It never overwrites what you wrote, and it never prints the contents of settings files, which often hold tokens. With `--global` the source is `~/.codex/AGENTS.md` (Codex is the one agent that cannot include another file, so the shared rules live where it reads them) and only the safe edits are made: an added include and the Gemini settings key. Behaviour that aisle has not confirmed against a real agent is marked *unverified*.

## tmux behaviour

- A session aisle starts is named `<engine>-<project>-<id8>` and labelled with `@aisle_engine` / `@aisle_id`, so it is linked back to its conversation. `aisle new claude` starts Claude Code with a session ID aisle picks (`--session-id`), so even a brand-new conversation is linked from its first message.
- A tmux session aisle did not start is linked to a conversation only when a process in it runs that agent's own resume command naming it (for example `claude --resume <id>` or `codex resume <id>`). It is marked *not started by aisle*. Everything else is listed separately as *unmanaged*; aisle does not guess.
- Inside tmux aisle switches the client; outside it attaches.

### Worktrees

`aisle new <engine> --worktree` (or `--worktree=fix-login`) starts the agent in a new git worktree: `<repo>/.worktrees/<name>` on a new branch `aisle/<name>` cut from `HEAD`, in the same subdirectory you were in. Uncommitted changes stay in your checkout. The directory is excluded through `.git/info/exclude`, so the repository itself does not change. It works the same for all four agents. aisle never deletes a worktree: `git worktree remove .worktrees/<name>` does when you are done.

### Live status

For every linked tmux session aisle shows what the agent is doing, refreshed every 1.5 s in the navigator and shown in `aisle list`:

| Status | Meaning |
|---|---|
| ⚑ needs you | a permission prompt, question or menu is open — these sessions are listed first |
| ◐ working | a spinner is shown, or the screen changed since the last look |
| ● idle | the agent finished and waits for your next prompt |
| ○ exited | the agent quit; the pane is back at the shell |

Status is read from the screen (`tmux capture-pane`), because tmux's activity timestamp cannot tell an agent that redraws while idle from one that works. It is a best effort: a prompt aisle does not recognise shows as idle.

## Configuration

Optional, at `~/.config/aisle/config.toml` (or `$AISLE_CONFIG`):

```toml
# engines to show, in this order
adapters = ["claude", "codex", "gemini", "agy"]
# where to look for a project when an agent stored only its name
project_roots = ["~/src", "~/work"]
# prompts never used as a session title (slash commands are always skipped)
ignore_prompts = ["yes", "ok", "continue"]
summary_length = 80
```

## Roadmap

Tracked in [milestones](https://github.com/mashkovd/aisle/milestones).

## License

MIT
