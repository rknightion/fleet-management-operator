#!/usr/bin/env python3
"""Tests for backlog-guard.py. Run: python3 .claude/hooks/backlog-guard_test.py

Both directions matter. A guard that blocks everything passes a
"does it block the bad form?" test and makes the tracker unusable, so the safe
forms are asserted to exit 0 just as hard as the unsafe forms are asserted to
exit 2.

The denied flags are built by concatenation ("--" + "notes") on purpose: the
guard inspects the Bash command string, so a literal spelling here would make
the hook block the very command that runs these tests.
"""

import json
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
HOOK = os.path.join(ROOT, ".claude", "hooks", "backlog-guard.py")
env = dict(os.environ, CLAUDE_PROJECT_DIR=ROOT)

N = "--" + "notes"
P = "--" + "plan"

cases = [
    # --- must block: the silent section-replacing flags -------------------
    ("bare notes flag",       {"tool_name": "Bash", "tool_input": {"command": f"backlog task edit FMO-0001 {N} hi"}}, 2),
    ("bare plan flag",        {"tool_name": "Bash", "tool_input": {"command": f"backlog task edit FMO-0001 {P} hi"}}, 2),
    ("equals form",           {"tool_name": "Bash", "tool_input": {"command": f"backlog task edit FMO-0001 {N}=hi"}}, 2),
    ("flag at end of line",   {"tool_name": "Bash", "tool_input": {"command": f"backlog task edit FMO-0001 {N}"}}, 2),
    ("flag mid-pipeline",     {"tool_name": "Bash", "tool_input": {"command": f"cd x && backlog task edit FMO-0001 {P} hi | tee log"}}, 2),

    # --- must allow: the safe forms and ordinary tracker use --------------
    ("append-notes allowed",  {"tool_name": "Bash", "tool_input": {"command": "backlog task edit FMO-0001 --append-notes hi"}}, 0),
    ("append-plan allowed",   {"tool_name": "Bash", "tool_input": {"command": "backlog task edit FMO-0001 --append-plan hi"}}, 0),
    ("task list allowed",     {"tool_name": "Bash", "tool_input": {"command": "backlog task list --plain"}}, 0),
    ("task view allowed",     {"tool_name": "Bash", "tool_input": {"command": "backlog task view FMO-0001 --plain"}}, 0),
    ("finalize in one call",  {"tool_name": "Bash", "tool_input": {"command": "backlog task edit FMO-0001 --check-ac 1 -s Done"}}, 0),
    ("doc update allowed",    {"tool_name": "Bash", "tool_input": {"command": "backlog doc update doc-0002 --content x"}}, 0),
    ("non-backlog cmd",       {"tool_name": "Bash", "tool_input": {"command": f"mytool {N} foo"}}, 0),
    ("make test allowed",     {"tool_name": "Bash", "tool_input": {"command": "make lint && make test"}}, 0),

    # --- must block: hand-editing CLI-owned markdown ----------------------
    ("edit task md",          {"tool_name": "Edit",  "tool_input": {"file_path": f"{ROOT}/backlog/tasks/FMO-0001 - x.md"}}, 2),
    ("write doc md",          {"tool_name": "Write", "tool_input": {"file_path": f"{ROOT}/backlog/docs/doc-0002 - Wave-operating-model.md"}}, 2),
    ("edit completed md",     {"tool_name": "Edit",  "tool_input": {"file_path": f"{ROOT}/backlog/completed/FMO-0009 - z.md"}}, 2),
    ("edit archive md",       {"tool_name": "Edit",  "tool_input": {"file_path": f"{ROOT}/backlog/archive/tasks/FMO-0009 - z.md"}}, 2),

    # --- must allow: config.yml and every ordinary repo file --------------
    ("config.yml allowed",    {"tool_name": "Edit",  "tool_input": {"file_path": f"{ROOT}/backlog/config.yml"}}, 0),
    ("go source allowed",     {"tool_name": "Edit",  "tool_input": {"file_path": f"{ROOT}/internal/controller/pipeline_controller.go"}}, 0),
    ("crd types allowed",     {"tool_name": "Edit",  "tool_input": {"file_path": f"{ROOT}/api/v1alpha1/pipeline_types.go"}}, 0),
    ("chart values allowed",  {"tool_name": "Write", "tool_input": {"file_path": f"{ROOT}/charts/fleet-management-operator/values.yaml"}}, 0),
    ("AGENTS.md allowed",     {"tool_name": "Write", "tool_input": {"file_path": f"{ROOT}/AGENTS.md"}}, 0),
]

fails = 0
for name, payload, want in cases:
    r = subprocess.run([sys.executable, HOOK], input=json.dumps(payload),
                       capture_output=True, text=True, env=env)
    ok = r.returncode == want
    fails += not ok
    print(f"{'PASS' if ok else 'FAIL'}  exit={r.returncode} want={want}  {name}")

# A guard that dies on a payload it cannot parse would wedge every tool call.
r = subprocess.run([sys.executable, HOOK], input="not json", capture_output=True, text=True, env=env)
ok = r.returncode == 0
fails += not ok
print(f"{'PASS' if ok else 'FAIL'}  exit={r.returncode} want=0  garbage stdin never blocks")

total = len(cases) + 1
print(f"\n{total - fails}/{total} passed")
sys.exit(1 if fails else 0)
