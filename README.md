# aisle — cross-agent session navigator

**aisle discovers the AI coding sessions you already have — whichever agent created them — and lets you find and resume them from one place.**

You have months of conversations spread across Claude Code, Codex, Gemini CLI and Antigravity, each hidden in its own storage format and its own `--resume` flag. aisle reads all of them, shows one list newest-first, and drops you back into the right conversation, in the right directory, inside tmux.

![aisle: list sessions from four agents, filter, resume a Gemini conversation in tmux](demo/aisle.gif)

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
aisle doctor          # agents found, storage paths, formats, warnings
```

In the navigator: `enter` resumes, `/` filters titles and projects, `f` searches the full text of your conversations, `n` starts a new session, `p` groups by project, `?` shows all keys. Shortcuts like `c3` or `x1` are shown for orientation; on the command line always use the stable session ID.

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

## tmux behaviour

- A session aisle starts is named `<engine>-<project>-<id8>` and labelled with `@aisle_engine` / `@aisle_id`, so it is linked back to its conversation.
- tmux sessions aisle did not start are listed separately as *unmanaged*. aisle does not guess which conversation they hold.
- Inside tmux aisle switches the client; outside it attaches.

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

- **0.3** `aisle rules` — one `AGENTS.md` shared by all agents
- **0.4** live status of running sessions, git worktrees per session

## License

MIT
