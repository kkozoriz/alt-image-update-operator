#!/usr/bin/env python3
"""
Ralph loop for Codex agents.

It repeatedly assigns one queued task from tasks.json to a non-interactive Codex
run. Codex must implement exactly that task and update tasks.json/progress.txt.

Default mode is serial. This is intentional: it avoids concurrent writes to the
same repository and keeps the thesis project reviewable.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional, Tuple

READY_STATUSES = {"queued"}
RETRYABLE_STATUSES = {"queued", "in_progress", "blocked", "failed"}
TERMINAL_STATUSES = {"done", "blocked", "failed", "skipped"}


def utcnow() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def eprint(*args: object) -> None:
    print(*args, file=sys.stderr)


def load_json(path: Path) -> Dict[str, Any]:
    try:
        with path.open("r", encoding="utf-8") as f:
            data = json.load(f)
    except FileNotFoundError:
        raise SystemExit(f"tasks file not found: {path}")
    except json.JSONDecodeError as exc:
        raise SystemExit(f"invalid JSON in {path}: {exc}") from exc

    if not isinstance(data, dict) or not isinstance(data.get("tasks"), list):
        raise SystemExit(f"{path} must be an object with a 'tasks' array")
    return data


def atomic_write_json(path: Path, data: Dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(prefix=f".{path.name}.", suffix=".tmp", dir=str(path.parent))
    tmp = Path(tmp_name)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump(data, f, ensure_ascii=False, indent=2)
            f.write("\n")
        tmp.replace(path)
    finally:
        if tmp.exists():
            tmp.unlink(missing_ok=True)


def append_progress(progress_path: Path, line: str) -> None:
    progress_path.parent.mkdir(parents=True, exist_ok=True)
    with progress_path.open("a", encoding="utf-8") as f:
        f.write(f"[{utcnow()}] {line}\n")


def task_map(tasks: Iterable[Dict[str, Any]]) -> Dict[str, Dict[str, Any]]:
    out: Dict[str, Dict[str, Any]] = {}
    for task in tasks:
        tid = task.get("id")
        if isinstance(tid, str):
            out[tid] = task
    return out


def deps_done(task: Dict[str, Any], by_id: Dict[str, Dict[str, Any]]) -> bool:
    deps = task.get("dependencies", [])
    if not isinstance(deps, list):
        return False
    for dep in deps:
        if by_id.get(str(dep), {}).get("status") != "done":
            return False
    return True


def priority_key(task: Dict[str, Any]) -> Tuple[int, str]:
    # Higher priority number goes first. Missing priority goes last.
    raw_priority = task.get("priority", -9999)
    try:
        priority = int(raw_priority)
    except (TypeError, ValueError):
        priority = -9999
    return -priority, str(task.get("id", ""))


def claim_next_task(data: Dict[str, Any], worker_id: str) -> Optional[Dict[str, Any]]:
    tasks = data["tasks"]
    by_id = task_map(tasks)
    candidates = [
        task for task in tasks
        if isinstance(task, dict)
        and task.get("status") in READY_STATUSES
        and deps_done(task, by_id)
    ]
    if not candidates:
        return None

    task = sorted(candidates, key=priority_key)[0]
    task["status"] = "in_progress"
    task["assigned_to"] = worker_id
    task["started_at"] = utcnow()
    task["completed_at"] = None
    task["result_summary"] = None
    task["attempts"] = int(task.get("attempts") or 0) + 1
    return task


def claim_specific_task(data: Dict[str, Any], task_id: str, worker_id: str) -> Optional[Dict[str, Any]]:
    task = get_task(data, task_id)
    if not task:
        raise SystemExit(f"task not found: {task_id}")
    by_id = task_map(t for t in data.get("tasks", []) if isinstance(t, dict))
    if task.get("status") not in READY_STATUSES:
        return None
    if not deps_done(task, by_id):
        waiting = [
            str(dep)
            for dep in task.get("dependencies", [])
            if by_id.get(str(dep), {}).get("status") != "done"
        ]
        raise SystemExit(f"task {task_id} dependencies are not done: {', '.join(waiting)}")

    task["status"] = "in_progress"
    task["assigned_to"] = worker_id
    task["started_at"] = utcnow()
    task["completed_at"] = None
    task["result_summary"] = None
    task["validation_result"] = None
    task["attempts"] = int(task.get("attempts") or 0) + 1
    return task


def reset_task_for_retry(data: Dict[str, Any], task_id: str, *, force: bool = False) -> Dict[str, Any]:
    task = get_task(data, task_id)
    if not task:
        raise SystemExit(f"task not found: {task_id}")

    status = str(task.get("status"))
    if status == "done" and not force:
        raise SystemExit(f"task {task_id} is done; use --force to reset a done task")
    if status not in RETRYABLE_STATUSES and not force:
        raise SystemExit(f"task {task_id} has status {status!r}; use --force to reset it")

    task["status"] = "queued"
    task["assigned_to"] = None
    task["started_at"] = None
    task["completed_at"] = None
    task["result_summary"] = None
    task["validation_result"] = None
    return task


def get_task(data: Dict[str, Any], task_id: str) -> Optional[Dict[str, Any]]:
    for task in data.get("tasks", []):
        if isinstance(task, dict) and task.get("id") == task_id:
            return task
    return None


def summarize_pool(data: Dict[str, Any]) -> Dict[str, int]:
    counts: Dict[str, int] = {}
    for task in data.get("tasks", []):
        status = str(task.get("status", "unknown")) if isinstance(task, dict) else "invalid"
        counts[status] = counts.get(status, 0) + 1
    return counts


def no_runnable_tasks(data: Dict[str, Any]) -> bool:
    tasks = [t for t in data.get("tasks", []) if isinstance(t, dict)]
    by_id = task_map(tasks)
    return not any(t.get("status") in READY_STATUSES and deps_done(t, by_id) for t in tasks)


def all_done_or_terminal(data: Dict[str, Any]) -> bool:
    tasks = [t for t in data.get("tasks", []) if isinstance(t, dict)]
    return bool(tasks) and all(str(t.get("status")) in TERMINAL_STATUSES for t in tasks)


def render_prompt(worker_prompt: str, task_id: str, worker_id: str, project_root: Path, tasks_path: Path) -> str:
    return (
        worker_prompt.rstrip()
        + "\n\n---\n"
        + f"ASSIGNED_TASK_ID: {task_id}\n"
        + f"WORKER_ID: {worker_id}\n"
        + f"PROJECT_ROOT: {project_root}\n"
        + f"TASKS_FILE: {tasks_path}\n"
    )


def run_codex(
    *,
    codex_bin: str,
    project_root: Path,
    model: str,
    reasoning_effort: str,
    sandbox: str,
    approval: str,
    prompt: str,
    log_path: Path,
    last_message_path: Path,
    extra_args: List[str],
    stream_logs: bool,
) -> int:
    cmd = [
        codex_bin,
        "exec",
        "--cd",
        str(project_root),
        "--model",
        model,
        "--config",
        f'model_reasoning_effort="{reasoning_effort}"',
        "--sandbox",
        sandbox,
        "--output-last-message",
        str(last_message_path),
        "--skip-git-repo-check",
    ]
    cmd.extend(extra_args)
    cmd.append("-")

    log_path.parent.mkdir(parents=True, exist_ok=True)
    with log_path.open("w", encoding="utf-8") as log:
        log.write(f"$ {' '.join(cmd)}\n\n")
        log.flush()

        proc = subprocess.Popen(
            cmd,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            cwd=str(project_root),
        )

        assert proc.stdin is not None
        proc.stdin.write(prompt)
        proc.stdin.close()

        assert proc.stdout is not None
        for line in proc.stdout:
            log.write(line)
            log.flush()
            if stream_logs:
                print(line, end="", flush=True)

        proc.wait()
        log.write(f"\n\n[ralph] exit_code={proc.returncode}\n")
    return proc.returncode


def mark_task_failed_if_still_in_progress(tasks_path: Path, task_id: str, worker_id: str, reason: str) -> None:
    data = load_json(tasks_path)
    task = get_task(data, task_id)
    if not task:
        return
    if task.get("status") == "in_progress" and task.get("assigned_to") == worker_id:
        task["status"] = "failed"
        task["completed_at"] = utcnow()
        task["result_summary"] = reason
        atomic_write_json(tasks_path, data)


def parse_args(argv: Optional[List[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run a Ralph loop over tasks.json using Codex CLI")
    parser.add_argument("--project-root", default=".", help="Repository root")
    parser.add_argument("--tasks", default="tasks.json", help="Path to tasks.json, relative to project root unless absolute")
    parser.add_argument("--progress", default="progress.txt", help="Path to progress.txt, relative to project root unless absolute")
    parser.add_argument("--worker-prompt", default="prompts/03_worker_prompt.md", help="Path to worker prompt")
    parser.add_argument("--model", default="gpt-5.5", help="Codex model, e.g. gpt-5.5 or gpt-5.4")
    parser.add_argument("--reasoning-effort", default="high", choices=["low", "medium", "high", "xhigh"], help="Codex model reasoning effort for worker runs")
    parser.add_argument("--sandbox", default="workspace-write", choices=["read-only", "workspace-write", "danger-full-access"], help="Codex sandbox mode")
    parser.add_argument("--approval", default="never", choices=["untrusted", "on-request", "never"], help="Deprecated compatibility option; codex-cli 0.128.0 no longer accepts an approval flag for exec.")
    parser.add_argument("--codex-bin", default="codex", help="Codex executable")
    parser.add_argument("--sleep", type=float, default=2.0, help="Seconds between iterations")
    parser.add_argument("--max-iterations", type=int, default=0, help="0 means unlimited until no runnable tasks")
    parser.add_argument("--stop-on-failure", action="store_true", help="Stop if Codex exits non-zero")
    parser.add_argument("--retry-task", help="Reset a failed, blocked, in-progress, or queued task and run only that task")
    parser.add_argument("--reset-task", help="Reset a failed, blocked, in-progress, or queued task to queued and exit")
    parser.add_argument("--force", action="store_true", help="Allow --retry-task or --reset-task to reset a done or otherwise non-retryable task")
    parser.add_argument("--extra-codex-arg", action="append", default=[], help="Extra argument passed to codex exec; repeatable")
    parser.add_argument("--no-stream-logs", action="store_true", help="Do not stream Codex output to the terminal; only write .ralph logs")
    return parser.parse_args(argv)


def main(argv: Optional[List[str]] = None) -> int:
    args = parse_args(argv)
    project_root = Path(args.project_root).resolve()
    tasks_path = Path(args.tasks)
    if not tasks_path.is_absolute():
        tasks_path = project_root / tasks_path
    progress_path = Path(args.progress)
    if not progress_path.is_absolute():
        progress_path = project_root / progress_path
    worker_prompt_path = Path(args.worker_prompt)
    if not worker_prompt_path.is_absolute():
        worker_prompt_path = project_root / worker_prompt_path

    codex_path = shutil.which(args.codex_bin)
    if not codex_path:
        eprint(f"Codex CLI not found: {args.codex_bin}")
        eprint("Install it, for example: npm i -g @openai/codex")
        return 2

    if not worker_prompt_path.exists():
        eprint(f"worker prompt not found: {worker_prompt_path}")
        return 2

    if args.retry_task and args.reset_task:
        eprint("--retry-task and --reset-task are mutually exclusive")
        return 2

    if args.retry_task or args.reset_task:
        retry_id = str(args.retry_task or args.reset_task)
        data = load_json(tasks_path)
        task = reset_task_for_retry(data, retry_id, force=args.force)
        atomic_write_json(tasks_path, data)
        append_progress(progress_path, f"RALPH RESET {retry_id}: status=queued previous task reset for retry")
        print(f"Reset {retry_id} to queued")
        if args.reset_task:
            return 0
        args.max_iterations = 1

    worker_prompt = worker_prompt_path.read_text(encoding="utf-8")
    ralph_dir = project_root / ".ralph"
    ralph_dir.mkdir(parents=True, exist_ok=True)

    worker_id = f"ralph-{os.getpid()}"
    append_progress(progress_path, f"RALPH START worker_id={worker_id} model={args.model} reasoning_effort={args.reasoning_effort}")

    iteration = 0
    while True:
        if args.max_iterations and iteration >= args.max_iterations:
            append_progress(progress_path, f"RALPH STOP max_iterations={args.max_iterations}")
            print(f"Reached max iterations: {args.max_iterations}")
            return 0
        iteration += 1

        data = load_json(tasks_path)
        counts = summarize_pool(data)
        print(f"[ralph] iteration={iteration} pool={counts}")

        if all_done_or_terminal(data):
            append_progress(progress_path, f"RALPH STOP all tasks terminal pool={counts}")
            print("All tasks are terminal. Ralph loop stopped.")
            return 0

        if no_runnable_tasks(data):
            append_progress(progress_path, f"RALPH STOP no runnable tasks pool={counts}")
            print("No runnable queued tasks. Some tasks may be blocked, failed, or waiting for dependencies.")
            return 0

        if args.retry_task:
            task = claim_specific_task(data, str(args.retry_task), worker_id)
        else:
            task = claim_next_task(data, worker_id)
        if task is None:
            append_progress(progress_path, f"RALPH STOP no claimable task pool={counts}")
            print("No claimable task.")
            return 0

        task_id = str(task["id"])
        title = str(task.get("title", ""))
        atomic_write_json(tasks_path, data)
        append_progress(progress_path, f"RALPH ASSIGN {task_id}: {title}")

        prompt = render_prompt(worker_prompt, task_id, worker_id, project_root, tasks_path)
        log_path = ralph_dir / f"{iteration:04d}_{task_id}.log"
        last_message_path = ralph_dir / f"{iteration:04d}_{task_id}_last.md"

        print(f"[ralph] running Codex for {task_id}: {title}")
        rc = run_codex(
            codex_bin=codex_path,
            project_root=project_root,
            model=args.model,
            reasoning_effort=args.reasoning_effort,
            sandbox=args.sandbox,
            approval=args.approval,
            prompt=prompt,
            log_path=log_path,
            last_message_path=last_message_path,
            extra_args=list(args.extra_codex_arg or []),
            stream_logs=not args.no_stream_logs,
        )

        if rc != 0:
            reason = f"Codex exited with code {rc}; see {log_path}"
            append_progress(progress_path, f"RALPH CODEX_FAILURE {task_id}: {reason}")
            mark_task_failed_if_still_in_progress(tasks_path, task_id, worker_id, reason)
            if args.stop_on_failure:
                print(reason)
                return rc
        else:
            # If the agent did not close its own task, mark this as failed to avoid an infinite loop.
            data_after = load_json(tasks_path)
            task_after = get_task(data_after, task_id)
            if task_after and task_after.get("status") == "in_progress" and task_after.get("assigned_to") == worker_id:
                reason = "Codex returned success but left the task in_progress; task marked failed by Ralph guard"
                task_after["status"] = "failed"
                task_after["completed_at"] = utcnow()
                task_after["result_summary"] = reason
                atomic_write_json(tasks_path, data_after)
                append_progress(progress_path, f"RALPH GUARD_FAIL {task_id}: {reason}")

        time.sleep(max(0.0, args.sleep))


if __name__ == "__main__":
    raise SystemExit(main())
