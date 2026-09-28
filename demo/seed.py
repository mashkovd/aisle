#!/usr/bin/env python3
"""Build a synthetic $HOME with Claude, Codex, Gemini and agy history for the README demo.

Usage: demo/seed.py <home-dir>. Timestamps are relative to now so the list reads "5m ago".
Nothing here comes from a real machine.
"""
import json, os, sqlite3, sys, uuid
from datetime import datetime, timedelta, timezone

home = os.path.abspath(sys.argv[1])
now = datetime.now(timezone.utc)
work = os.path.join(home, "work")

def iso(dt): return dt.isoformat(timespec="milliseconds").replace("+00:00", "Z")
def ago(**kw): return now - timedelta(**kw)
def write(path, lines):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        for l in lines: f.write(json.dumps(l) + "\n")

for p in ["api", "web", "infra", "mobile"]:
    os.makedirs(os.path.join(work, p), exist_ok=True)

# Claude Code
claude = [
    ("api", "Fix flaky login test in CI", ago(minutes=4)),
    ("web", "Migrate checkout page to server components", ago(hours=3)),
    ("infra", "Terraform: split staging and prod state", ago(days=2)),
    ("api", "Rate limiter for public endpoints", ago(days=9)),
    ("mobile", "Crash on cold start after deploy", ago(days=30)),
]
for proj, title, t in claude:
    sid = str(uuid.uuid4()); cwd = os.path.join(work, proj)
    slug = cwd.replace("/", "-")
    write(os.path.join(home, ".claude/projects", slug, sid + ".jsonl"), [
        {"type": "user", "message": {"role": "user", "content": title.lower()}, "timestamp": iso(t - timedelta(minutes=20)), "cwd": cwd, "sessionId": sid},
        {"type": "assistant", "message": {"role": "assistant", "content": []}, "timestamp": iso(t), "cwd": cwd, "sessionId": sid},
        {"type": "ai-title", "aiTitle": title, "sessionId": sid},
    ])

# Codex CLI
index = []
for proj, title, t in [("web", "Add dark mode toggle", ago(minutes=40)), ("api", "Audit auth middleware", ago(days=1)), ("infra", "Why is the deploy pipeline slow?", ago(days=5))]:
    sid = str(uuid.uuid4()); d = t.strftime("%Y/%m/%d")
    path = os.path.join(home, ".codex/sessions", d, f"rollout-{t.strftime('%Y-%m-%dT%H-%M-%S')}-{sid}.jsonl")
    write(path, [
        {"timestamp": iso(t), "type": "session_meta", "payload": {"id": sid, "cwd": os.path.join(work, proj), "source": "cli", "thread_source": "user"}},
        {"timestamp": iso(t), "type": "event_msg", "payload": {"type": "user_message", "message": title}},
    ])
    os.utime(path, (t.timestamp(), t.timestamp()))
    index.append({"id": sid, "thread_name": title, "updated_at": iso(t)})
write(os.path.join(home, ".codex/session_index.jsonl"), index)

# Gemini CLI (0.46 JSONL)
for proj, prompt, t in [("mobile", "Explain the release signing setup", ago(hours=6)), ("infra", "Draft a runbook for the deploy rollback", ago(days=3))]:
    sid = str(uuid.uuid4()); d = os.path.join(home, ".gemini/tmp", proj)
    os.makedirs(d, exist_ok=True)
    open(os.path.join(d, ".project_root"), "w").write(os.path.join(work, proj))
    write(os.path.join(d, "chats", f"session-{t.strftime('%Y-%m-%dT%H-%M')}-{sid[:8]}.jsonl"), [
        {"sessionId": sid, "projectHash": "demo", "startTime": iso(t), "lastUpdated": iso(t), "kind": "main"},
        {"id": "u1", "timestamp": iso(t), "type": "user", "content": [{"text": prompt}]},
        {"id": "g1", "timestamp": iso(t), "type": "gemini", "content": "Sure."},
    ])

# Antigravity CLI
db_dir = os.path.join(home, ".gemini/antigravity-cli"); os.makedirs(db_dir, exist_ok=True)
schema = open(os.path.join(os.path.dirname(__file__), "..", "testdata/agy/1.2/schema.sql")).read()
db = sqlite3.connect(os.path.join(db_dir, "conversation_summaries.db")); db.executescript(schema)
for proj, title, t in [("web", "Design system token cleanup", ago(hours=20)), ("api", "Deploy preview environments per PR", ago(days=12))]:
    ts = t.strftime("%Y-%m-%d %H:%M:%S.%f+00:00")
    db.execute("INSERT INTO conversation_summaries (conversation_id,title,preview,step_count,last_modified_time,workspace_uris,last_user_input_time) VALUES (?,?,?,?,?,?,?)",
               (str(uuid.uuid4()), title, title, 5, ts, json.dumps(["file://" + os.path.join(work, proj)]), ts))
db.commit()
