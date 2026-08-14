#!/usr/bin/env python3
"""PreToolUse guard for the Backlog.md tracker.

Two upstream footguns are silent and unrepairable, so they are denied here rather
than documented and hoped about (see AGENTS.md "Tracker rules (non-negotiable)"):

1. `backlog task edit` with the bare section-setting flags REPLACES the whole
   section. Another agent's writes vanish with no warning and exit 0. The
   `--append-*` forms are the safe ones.
2. Hand-editing task/doc/decision markdown breaks the HTML-comment section
   markers. The section is then silently dropped at exit 0 - still in the file,
   invisible to the CLI - until the next write destroys it for real. There is no
   repair command; `backlog doctor` only fixes duplicate task IDs.

Exit 0 allows. Exit 2 blocks and shows stderr to the agent.

WHAT THIS IS. A guardrail against an agent's honest mistake, not a security
boundary. It cannot evaluate the shell, so a value routed through a variable
across command lines, or built at runtime, will get past it. The backstop below
catches the common assignment case; anything cleverer is out of scope by
construction and should not be mistaken for coverage.

THREE FIXES over the first version, each confirmed against the live hook before
being changed - the originals were not theoretical:

* Bash could freely destroy CLI-owned markdown. Edit/Write/MultiEdit were
  guarded while `sed -i`, a `>` redirect and `python3 -c` all sailed through,
  which is the whole protection missing on the tool agents reach for most.
* Matching ran on the raw command string, so shell quoting decided the outcome.
  `backlog task edit X '--notes' hi` and the "..." form BOTH evaded the guard and
  reached the real CLI - the destructive write the hook exists to stop, waved
  through because an agent quoted its own flags, which is entirely normal. The
  same string matching fired on innocent commands in the other direction: it
  blocked a heredoc that merely mentioned the flag names in a comment. Both
  directions are fixed by tokenizing the way the shell does and only judging
  tokens of a segment that actually invokes `backlog`.
* Path matching was case-sensitive and unresolved. On macOS's default
  case-insensitive filesystem `.../Backlog/Tasks/x.md` is the SAME FILE and was
  allowed; symlinks were never resolved.
"""

import json
import os
import re
import shlex
import sys

SECTIONS = ("notes", "plan")

# Fallback only, for a command shlex cannot tokenize. Matches the flags as whole
# words; `--append-notes` does not match because the two characters before
# "notes" there are "d-", not "--".
BARE_SECTION_FLAG = re.compile(r"(?<![-\w])--(notes|plan)(?=[=\s]|$)")

# Files the CLI owns. config.yml is deliberately excluded - list-valued keys
# cannot be set through `backlog config set`, so hand-editing it is the
# documented path. IGNORECASE because a case-insensitive filesystem resolves
# Backlog/Tasks to the same inode as backlog/tasks.
CLI_OWNED = re.compile(
    r"(^|/)backlog/(tasks|drafts|docs|decisions|milestones|completed|archive)/",
    re.IGNORECASE,
)

# Shell operators that end one command and start the next.
SEPARATORS = {";", "&&", "||", "|", "&", "\n"}
REDIRECTS = {">", ">>", ">|", "&>", ">&"}

# Commands that rewrite a file given to them as an argument. Read-only uses of
# sed/perl are common on task files, so those two count only with -i.
ALWAYS_MUTATING = {
    "tee", "truncate", "dd", "cp", "mv", "rm", "install", "ed", "ex", "patch",
    "python", "python3", "node", "ruby", "sh", "bash", "zsh", "vim", "vi", "nano",
}
MUTATING_WITH_INPLACE = {"sed", "perl", "awk"}


def deny(reason: str) -> None:
    print(reason, file=sys.stderr)
    sys.exit(2)


def deny_section_flag(flag: str) -> None:
    deny(
        f"BLOCKED: bare `--{flag}` silently REPLACES the entire {flag} section, "
        f"destroying any other session's writes with no warning and exit 0.\n"
        f"Use `--append-{flag}` instead. This is an open upstream bug, not a "
        f'misunderstanding - see AGENTS.md "Tracker rules".'
    )


def deny_hand_edit(how: str) -> None:
    deny(
        f"BLOCKED: {how} would hand-edit Backlog.md task/doc/decision markdown, "
        "which is CLI-owned. Section boundaries are HTML-comment markers; "
        "breaking one silently drops the section at exit 0 - the data stays in "
        "the file but is invisible to the CLI until the next write destroys it "
        "for real, and there is no repair command.\n"
        "Use `backlog task edit` / `backlog doc update --content` instead. "
        "(`backlog/config.yml` is exempt and may be edited by hand.)"
    )


def project_dir() -> str:
    return os.environ.get("CLAUDE_PROJECT_DIR") or os.getcwd()


def is_cli_owned(path: str) -> bool:
    """True if path lands in a CLI-owned directory, after resolving symlinks,
    `..`, duplicate separators and a relative base."""
    if not path or path.startswith("-"):
        return False
    candidate = os.path.expanduser(path)
    if not os.path.isabs(candidate):
        candidate = os.path.join(project_dir(), candidate)
    # realpath resolves symlinks and traversal; it is defined for paths that do
    # not exist yet, which is the normal case for a file about to be written.
    resolved = os.path.realpath(candidate).replace(os.sep, "/")
    resolved = re.sub(r"/{2,}", "/", resolved)
    return bool(CLI_OWNED.search(resolved))


def segments(tokens):
    """Split a token stream into command segments on shell operators."""
    current, out = [], []
    for tok in tokens:
        if tok in SEPARATORS:
            if current:
                out.append(current)
            current = []
        else:
            current.append(tok)
    if current:
        out.append(current)
    return out


def invokes_backlog(seg) -> bool:
    """A segment invokes the tracker only if `backlog` appears as its OWN token.

    This is what separates a real invocation from a mention. `echo 'backlog
    --notes is unsafe'` tokenizes to two tokens, neither of which is `backlog`,
    and `grep -- '--notes' backlog/` yields `backlog/` - so neither is judged.
    """
    return any(t == "backlog" or t.endswith("/backlog") for t in seg)


def check_bash(command: str) -> None:
    try:
        tokens = shlex.split(command, comments=False)
    except ValueError:
        # Unbalanced quotes, a heredoc, something shlex will not take. Fall back
        # to the blunt string match, which over-blocks rather than under-blocks.
        if "backlog" in command:
            m = BARE_SECTION_FLAG.search(command)
            if m:
                deny_section_flag(m.group(1))
        sys.exit(0)

    segs = segments(tokens)
    backlog_segs = [s for s in segs if invokes_backlog(s)]

    for seg in backlog_segs:
        for tok in seg:
            for name in SECTIONS:
                if tok == f"--{name}" or tok.startswith(f"--{name}="):
                    deny_section_flag(name)

    # Backstop for the assignment-then-splice shape
    # (`F=--notes; backlog task edit X $F hi`): only armed when a real backlog
    # invocation is present in the same command, which keeps it off innocent text.
    if backlog_segs:
        for tok in tokens:
            for name in SECTIONS:
                if f"--{name}" in tok and not tok.startswith(f"--append-{name}"):
                    deny_section_flag(name)

    # A shell command can rewrite CLI-owned markdown just as easily as the Edit
    # tool can. The backlog CLI writing its own files is of course fine.
    for seg in segs:
        if invokes_backlog(seg) or not seg:
            continue
        cmd = os.path.basename(seg[0])
        has_redirect = any(t in REDIRECTS for t in seg)
        inplace = any(t == "-i" or t.startswith("-i") for t in seg[1:])
        mutating = (
            has_redirect
            or cmd in ALWAYS_MUTATING
            or (cmd in MUTATING_WITH_INPLACE and inplace)
        )
        if mutating and any(is_cli_owned(t) for t in seg):
            deny_hand_edit(f"`{cmd}`")

    sys.exit(0)


def main() -> None:
    try:
        payload = json.load(sys.stdin)
    except (json.JSONDecodeError, ValueError):
        sys.exit(0)  # never block on a payload we cannot parse

    tool = payload.get("tool_name", "")
    ti = payload.get("tool_input") or {}

    if tool == "Bash":
        check_bash(ti.get("command", "") or "")
        sys.exit(0)

    if tool in ("Edit", "Write", "MultiEdit", "NotebookEdit"):
        path = ti.get("file_path") or ti.get("notebook_path") or ""
        if is_cli_owned(path):
            deny_hand_edit(f"`{tool}`")
        sys.exit(0)

    sys.exit(0)


if __name__ == "__main__":
    main()
