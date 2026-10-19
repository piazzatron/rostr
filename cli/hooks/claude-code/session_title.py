"""Claude Code hook that drafts an agent name for each new session and puts it in the session title.

Runs as two UserPromptSubmit hook entries, both acting only on a session's first prompt:

  session_title.py name    Tells Claude its name and sets the local session title (`sessionTitle`).
  session_title.py rename  Run with `asyncRewake`. For desktop / Remote Control sessions, waits a few seconds,
                           then exits 2 to wake Claude with an instruction to set the app's title with
                           `set_session_title`, since a hook's `sessionTitle` never reaches the app.

Both modes derive the name from the session ID, so they agree without sharing state.
"""

import glob
import hashlib
import json
import os
import sys
import time

AGENT_NAMES = ["Falcon", "Otter", "Juniper", "Comet", "Maple", "Pixel"]
REWAKE_DELAY_SECONDS = 5


def main():
    mode = sys.argv[1]
    hook_input = json.load(sys.stdin)
    session_id = hook_input["session_id"]

    if not is_first_prompt(hook_input):
        return 0

    agent_name = agent_name_for_session(session_id)

    if mode == "name":
        print(json.dumps({
            "hookSpecificOutput": {
                "hookEventName": "UserPromptSubmit",
                "sessionTitle": f"{agent_name} · {hook_input['prompt'][:40]}",
                "additionalContext": f"For this session you've been drafted as {agent_name}.",
            }
        }))
        return 0

    if mode == "rename":
        # Terminal sessions have no app-side title to set
        bridge_session_id = bridge_session_id_for(session_id)
        if not bridge_session_id:
            return 0

        time.sleep(REWAKE_DELAY_SECONDS)
        print(
            f'Call mcp__claude-code-remote__set_session_title with session_id "{bridge_session_id}" and title '
            f'"{agent_name} · <your 3-5 word summary of this conversation>". '
            "Reply with nothing else and do not mention the renaming.",
            file=sys.stderr,
        )
        return 2

    raise ValueError(f"unknown mode: {mode}")


def is_first_prompt(hook_input):
    """True when the transcript holds no earlier prompt typed by the user, so existing sessions are never renamed."""
    transcript_path = hook_input["transcript_path"]
    if not os.path.exists(transcript_path):
        return True

    with open(transcript_path) as transcript:
        for line in transcript:
            entry = json.loads(line)
            is_human_prompt = entry.get("type") == "user" and (entry.get("origin") or {}).get("kind") == "human"
            if is_human_prompt and entry.get("promptId") != hook_input.get("prompt_id"):
                return False
    return True


def agent_name_for_session(session_id):
    digest = int(hashlib.sha256(session_id.encode()).hexdigest(), 16)
    return AGENT_NAMES[digest % len(AGENT_NAMES)]


def bridge_session_id_for(session_id):
    """Server session ID for desktop / Remote Control sessions, from Claude Code's per-process session files."""
    for path in glob.glob(os.path.expanduser("~/.claude/sessions/*.json")):
        with open(path) as session_file:
            session = json.load(session_file)
        if session.get("sessionId") == session_id:
            return session.get("bridgeSessionId")
    return None


if __name__ == "__main__":
    sys.exit(main())
