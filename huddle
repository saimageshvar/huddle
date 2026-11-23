#!/usr/bin/env python3
"""huddle: answer Claude Code's AskUserQuestion from a separate terminal.

  huddle         run the TUI; it waits for questions on the socket
  huddle hook    PreToolUse hook: forwards the question, prints the answers
"""
import json
import os
import signal
import socket
import sys

SOCK = os.environ.get("HUDDLE_SOCK", os.path.expanduser("~/.huddle.sock"))


def recv_all(conn):
    chunks = []
    while chunk := conn.recv(65536):
        chunks.append(chunk)
    return b"".join(chunks)


def hook():
    payload = sys.stdin.read()
    if not os.path.exists(SOCK):
        return  # no TUI running: fall through to the built-in dialog
    try:
        with socket.socket(socket.AF_UNIX) as s:
            s.connect(SOCK)
            s.sendall(payload.encode())
            s.shutdown(socket.SHUT_WR)
            reply = json.loads(recv_all(s) or b"null")
    except (OSError, ValueError):
        return
    if not reply:
        return  # skipped in the TUI
    tool_input = json.loads(payload)["tool_input"]
    print(json.dumps({"hookSpecificOutput": {
        "hookEventName": "PreToolUse",
        "permissionDecision": "allow",
        "updatedInput": {**tool_input, **reply},
    }}))


def ask(q):
    print(f"\n\033[1m[{q.get('header', '')}] {q['question']}\033[0m")
    opts = q["options"]
    for i, o in enumerate(opts, 1):
        print(f"  {i}. {o['label']} - {o.get('description', '')}")
        if o.get("preview"):
            print("     \033[2m" + o["preview"].replace("\n", "\n     ") + "\033[0m")
    hint = "numbers, comma-separated" if q.get("multiSelect") else "a number"
    raw = input(f"Pick {hint}, or type free text: ").strip()
    picks = [p.strip() for p in raw.split(",")]
    if all(p.isdigit() and 1 <= int(p) <= len(opts) for p in picks):
        answer = ", ".join(opts[int(p) - 1]["label"] for p in picks)
    else:
        answer = raw
    notes = input("Notes (enter to skip): ").strip()
    return answer, notes


def serve():
    for sig in (signal.SIGTERM, signal.SIGHUP):
        signal.signal(sig, lambda *_: sys.exit())  # run the finally that removes the socket
    if os.path.exists(SOCK):
        os.unlink(SOCK)
    with socket.socket(socket.AF_UNIX) as srv:
        srv.bind(SOCK)
        srv.listen()
        print(f"huddle: waiting for questions on {SOCK}")
        try:
            while True:
                conn, _ = srv.accept()
                with conn:
                    payload = json.loads(recv_all(conn))
                    print(f"\n=== {payload.get('cwd', '?')}  session {payload.get('session_id', '?')[:8]}")
                    answers, annotations = {}, {}
                    for q in payload["tool_input"]["questions"]:
                        answer, notes = ask(q)
                        answers[q["question"]] = answer
                        if notes:
                            annotations[q["question"]] = {"notes": notes}
                    reply = {"answers": answers}
                    if annotations:
                        reply["annotations"] = annotations
                    conn.sendall(json.dumps(reply).encode())
                    print("sent.")
        finally:
            os.unlink(SOCK)


if __name__ == "__main__":
    hook() if sys.argv[1:] == ["hook"] else serve()
