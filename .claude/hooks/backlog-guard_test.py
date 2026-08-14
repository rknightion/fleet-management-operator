#!/usr/bin/env python3
"""Tests for backlog-guard.py. Run: python3 .claude/hooks/backlog-guard_test.py

Both directions matter. A guard that blocks everything passes a
"does it block the bad form?" test and makes the tracker unusable, so the safe
forms are asserted to exit 0 just as hard as the unsafe forms are asserted to
exit 2. The false-positive cases below are not padding: an over-firing guard
trains people to route around it, and one of them (a heredoc that merely
mentioned the flag names) blocked a real command during the migration.

The denied flags are built by concatenation ("--" + "notes") on purpose: the
guard inspects the Bash command string, so a literal spelling here would make
the hook block the very command that runs these tests.
"""

import json
import os
import subprocess
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
HOOK = os.path.join(ROOT, ".claude", "hooks", "backlog-guard.py")
env = dict(os.environ, CLAUDE_PROJECT_DIR=ROOT)

N = "--" + "notes"
P = "--" + "plan"

TASK = f"{ROOT}/backlog/tasks/FMO-0001 - x.md"
DOC = f"{ROOT}/backlog/docs/doc-0002 - Wave-operating-model.md"

cases = [
    # --- must block: the silent section-replacing flags -------------------
    ("bare notes flag",        {"tool_name": "Bash", "tool_input": {"command": f"backlog task edit FMO-0001 {N} hi"}}, 2),
    ("bare plan flag",         {"tool_name": "Bash", "tool_input": {"command": f"backlog task edit FMO-0001 {P} hi"}}, 2),
    ("equals form",            {"tool_name": "Bash", "tool_input": {"command": f"backlog task edit FMO-0001 {N}=hi"}}, 2),
    ("flag at end of line",    {"tool_name": "Bash", "tool_input": {"command": f"backlog task edit FMO-0001 {N}"}}, 2),
    ("flag mid-pipeline",      {"tool_name": "Bash", "tool_input": {"command": f"cd x && backlog task edit FMO-0001 {P} hi | tee log"}}, 2),

    # Quoting must not decide the outcome. Both of these reached the real CLI
    # under the first version of the guard - the destructive write waved through
    # because an agent quoted its own flag, which is entirely normal.
    ("single-quoted flag",     {"tool_name": "Bash", "tool_input": {"command": f"backlog task edit FMO-0001 '{N}' hi"}}, 2),
    ("double-quoted flag",     {"tool_name": "Bash", "tool_input": {"command": f'backlog task edit FMO-0001 "{N}" hi'}}, 2),
    ("quote-spliced flag",     {"tool_name": "Bash", "tool_input": {"command": f"backlog task edit FMO-0001 {N[:6]}''{N[6:]} hi"}}, 2),
    ("assignment then splice", {"tool_name": "Bash", "tool_input": {"command": f"F={N}; backlog task edit FMO-0001 $F hi"}}, 2),

    # --- must allow: the safe forms and ordinary tracker use --------------
    ("append-notes allowed",   {"tool_name": "Bash", "tool_input": {"command": "backlog task edit FMO-0001 --append-notes hi"}}, 0),
    ("append-plan allowed",    {"tool_name": "Bash", "tool_input": {"command": "backlog task edit FMO-0001 --append-plan hi"}}, 0),
    ("append with quotes",     {"tool_name": "Bash", "tool_input": {"command": "backlog task edit FMO-0001 --append-notes 'landed in abc1234'"}}, 0),
    ("task list allowed",      {"tool_name": "Bash", "tool_input": {"command": "backlog task list --plain"}}, 0),
    ("task view allowed",      {"tool_name": "Bash", "tool_input": {"command": "backlog task view FMO-0001 --plain"}}, 0),
    ("finalize in one call",   {"tool_name": "Bash", "tool_input": {"command": "backlog task edit FMO-0001 --check-ac 1 -s Done"}}, 0),
    ("doc update allowed",     {"tool_name": "Bash", "tool_input": {"command": "backlog doc update doc-0002 --content x"}}, 0),
    ("non-backlog cmd",        {"tool_name": "Bash", "tool_input": {"command": f"mytool {N} foo"}}, 0),
    ("make test allowed",      {"tool_name": "Bash", "tool_input": {"command": "make lint && make test"}}, 0),

    # --- must allow: MENTIONING the flags is not USING them ---------------
    ("flag inside a string",   {"tool_name": "Bash", "tool_input": {"command": f"echo 'backlog {N} is unsafe' >> /tmp/note.txt"}}, 0),
    ("grep for the flag",      {"tool_name": "Bash", "tool_input": {"command": f"grep -rn -- '{N}' backlog/"}}, 0),
    ("read a task file",       {"tool_name": "Bash", "tool_input": {"command": f"sed -n '1,5p' '{TASK}'"}}, 0),
    ("cat a task file",        {"tool_name": "Bash", "tool_input": {"command": f"cat '{TASK}'"}}, 0),
    ("git rm the dir",         {"tool_name": "Bash", "tool_input": {"command": "git rm -r --cached backlog/tasks"}}, 0),

    # --- must block: Bash rewriting CLI-owned markdown --------------------
    # The tool agents reach for most was the one with no protection at all.
    ("sed -i on task md",      {"tool_name": "Bash", "tool_input": {"command": f"sed -i '' 's/a/b/' '{TASK}'"}}, 2),
    ("redirect over doc md",   {"tool_name": "Bash", "tool_input": {"command": f"echo hi > '{DOC}'"}}, 2),
    ("append-redirect task",   {"tool_name": "Bash", "tool_input": {"command": f"echo hi >> '{TASK}'"}}, 2),
    ("python rewrite",         {"tool_name": "Bash", "tool_input": {"command": f"python3 -c \"open('{TASK}','w')\""}}, 2),
    ("mv over a task",         {"tool_name": "Bash", "tool_input": {"command": f"mv /tmp/x.md '{TASK}'"}}, 2),
    ("perl -i on doc",         {"tool_name": "Bash", "tool_input": {"command": f"perl -i -pe 's/a/b/' '{DOC}'"}}, 2),
    ("relative path write",    {"tool_name": "Bash", "tool_input": {"command": "echo hi > backlog/tasks/FMO-0001.md"}}, 2),

    # --- must block: hand-editing CLI-owned markdown via the file tools ---
    ("edit task md",           {"tool_name": "Edit",  "tool_input": {"file_path": TASK}}, 2),
    ("write doc md",           {"tool_name": "Write", "tool_input": {"file_path": DOC}}, 2),
    ("edit completed md",      {"tool_name": "Edit",  "tool_input": {"file_path": f"{ROOT}/backlog/completed/FMO-0009 - z.md"}}, 2),
    ("edit archive md",        {"tool_name": "Edit",  "tool_input": {"file_path": f"{ROOT}/backlog/archive/tasks/FMO-0009 - z.md"}}, 2),

    # --- must block: the same file reached by another spelling ------------
    # On a case-insensitive filesystem this IS the same inode, and it was allowed.
    ("case variant",           {"tool_name": "Edit",  "tool_input": {"file_path": f"{ROOT}/Backlog/Tasks/FMO-0001 - x.md"}}, 2),
    ("doubled slash",          {"tool_name": "Edit",  "tool_input": {"file_path": f"{ROOT}/backlog//tasks/FMO-0001 - x.md"}}, 2),
    ("traversal round-trip",   {"tool_name": "Edit",  "tool_input": {"file_path": f"{ROOT}/docs/../backlog/tasks/FMO-0001 - x.md"}}, 2),
    ("relative file_path",     {"tool_name": "Edit",  "tool_input": {"file_path": "backlog/tasks/FMO-0001 - x.md"}}, 2),

    # --- must allow: config.yml and every ordinary repo file --------------
    ("config.yml allowed",     {"tool_name": "Edit",  "tool_input": {"file_path": f"{ROOT}/backlog/config.yml"}}, 0),
    ("go source allowed",      {"tool_name": "Edit",  "tool_input": {"file_path": f"{ROOT}/internal/controller/pipeline_controller.go"}}, 0),
    ("crd types allowed",      {"tool_name": "Edit",  "tool_input": {"file_path": f"{ROOT}/api/v1alpha1/pipeline_types.go"}}, 0),
    ("chart values allowed",   {"tool_name": "Write", "tool_input": {"file_path": f"{ROOT}/charts/fleet-management-operator/values.yaml"}}, 0),
    ("AGENTS.md allowed",      {"tool_name": "Write", "tool_input": {"file_path": f"{ROOT}/AGENTS.md"}}, 0),
    ("the guard itself",       {"tool_name": "Write", "tool_input": {"file_path": HOOK}}, 0),

    # --- must not block: unknown tools, empty input -----------------------
    ("unknown tool",           {"tool_name": "WebFetch", "tool_input": {"url": "https://example.com"}}, 0),
    ("empty bash command",     {"tool_name": "Bash",  "tool_input": {"command": ""}}, 0),
    ("missing tool_input",     {"tool_name": "Bash"}, 0),
]


def run(payload):
    return subprocess.run([sys.executable, HOOK], input=json.dumps(payload),
                          capture_output=True, text=True, env=env).returncode


fails = 0
for name, payload, want in cases:
    got = run(payload)
    ok = got == want
    fails += not ok
    print(f"{'PASS' if ok else 'FAIL'}  exit={got} want={want}  {name}")

# A symlink into the tracker resolves to a CLI-owned file, so it must be denied.
with tempfile.TemporaryDirectory() as tmp:
    link = os.path.join(tmp, "shortcut.md")
    try:
        os.symlink(TASK, link)
        got = run({"tool_name": "Edit", "tool_input": {"file_path": link}})
        ok = got == 2
        fails += not ok
        print(f"{'PASS' if ok else 'FAIL'}  exit={got} want=2  symlink into backlog/tasks")
    except OSError as exc:  # pragma: no cover
        print(f"SKIP  symlink case ({exc})")

# A guard that dies on a payload it cannot parse would wedge every tool call.
r = subprocess.run([sys.executable, HOOK], input="not json", capture_output=True, text=True, env=env)
ok = r.returncode == 0
fails += not ok
print(f"{'PASS' if ok else 'FAIL'}  exit={r.returncode} want=0  garbage stdin never blocks")

total = len(cases) + 2
print(f"\n{total - fails}/{total} passed")
sys.exit(1 if fails else 0)
