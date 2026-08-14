package httpapi

const workerRuntimeDriver = `#!/usr/bin/env python3
import argparse
import asyncio
import contextlib
import os
import signal
import sys


BOXLITE_MIN_VERSION = (0, 9, 0)


def version_tuple(value):
    parts = []
    for token in value.split("."):
        number = "".join(character for character in token if character.isdigit())
        if not number:
            break
        parts.append(int(number))
    return tuple((parts + [0, 0, 0])[:3])


async def copy_stream(stream, destination):
    if stream is None:
        return
    async for chunk in stream:
        if isinstance(chunk, str):
            chunk = chunk.encode("utf-8", "replace")
        destination.write(chunk)
        destination.flush()


def terminal_size():
    try:
        size = os.get_terminal_size(sys.stdin.fileno())
        return size.lines, size.columns
    except OSError:
        return 30, 120


async def boxlite_runtime():
    import boxlite

    if version_tuple(boxlite.__version__) < BOXLITE_MIN_VERSION:
        raise RuntimeError("BoxLite 0.9.0 or newer is required")
    return boxlite, boxlite.Boxlite.default()


async def boxlite_box(runtime, target):
    box = await runtime.get(target)
    if box is None:
        raise RuntimeError("BoxLite sandbox not found: " + target)
    return box


async def boxlite_exec(args):
    _, runtime = await boxlite_runtime()
    box = await boxlite_box(runtime, args.target)
    await box.start()
    execution = await box.exec(
        args.command[0],
        args.command[1:] or None,
        None,
        False,
        user="0:0",
        cwd=args.workdir,
    )
    stdin = execution.stdin()
    if stdin is not None:
        if args.stdin:
            payload = sys.stdin.buffer.read()
            if payload:
                await stdin.send_input(payload)
        await stdin.close()
    await asyncio.gather(
        copy_stream(execution.stdout(), sys.stdout.buffer),
        copy_stream(execution.stderr(), sys.stderr.buffer),
    )
    result = await execution.wait()
    return result.exit_code


async def boxlite_terminal_input(execution):
    stdin = execution.stdin()
    if stdin is None:
        return
    try:
        while True:
            chunk = await asyncio.to_thread(os.read, sys.stdin.fileno(), 8192)
            if not chunk:
                break
            await stdin.send_input(chunk)
    finally:
        await stdin.close()


async def boxlite_terminal(args):
    _, runtime = await boxlite_runtime()
    box = await boxlite_box(runtime, args.target)
    await box.start()
    execution = await box.exec(
        "env",
        [
            "HOME=/root",
            "USER=root",
            "LOGNAME=root",
            "TERM=xterm-256color",
            "sh",
            "-c",
            "cd /root 2>/dev/null || cd /; if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi",
        ],
        None,
        True,
        user="0:0",
    )

    def resize():
        rows, columns = terminal_size()
        asyncio.get_running_loop().create_task(execution.resize_tty(rows, columns))

    loop = asyncio.get_running_loop()
    if hasattr(signal, "SIGWINCH"):
        loop.add_signal_handler(signal.SIGWINCH, resize)
    resize()
    input_task = asyncio.create_task(boxlite_terminal_input(execution))
    try:
        await asyncio.gather(
            copy_stream(execution.stdout(), sys.stdout.buffer),
            copy_stream(execution.stderr(), sys.stderr.buffer),
        )
        result = await execution.wait()
        return result.exit_code
    finally:
        input_task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await input_task


async def run_boxlite(args):
    boxlite, runtime = await boxlite_runtime()
    if args.action == "probe":
        if not os.path.exists("/dev/kvm"):
            raise RuntimeError("/dev/kvm is unavailable")
        print(boxlite.__version__)
        return 0
    if args.action == "create":
        network = boxlite.NetworkSpec(mode="disabled" if args.network == "none" else "enabled")
        options = boxlite.BoxOptions(
            image=args.image,
            cpus=args.cpus,
            memory_mib=args.memory,
            working_dir=args.workdir,
            network=network,
            auto_remove=False,
            auto_stop=0,
            auto_delete=0,
            auto_resume=False,
            detach=True,
            user="0:0",
        )
        box, _ = await runtime.get_or_create(options, name=args.target)
        await box.start()
        print(args.target)
        return 0
    if args.action == "inspect":
        await boxlite_box(runtime, args.target)
        print(args.target)
        return 0
    if args.action == "start":
        box = await boxlite_box(runtime, args.target)
        await box.start()
        print(args.target)
        return 0
    if args.action == "stop":
        box = await boxlite_box(runtime, args.target)
        await box.stop()
        print(args.target)
        return 0
    if args.action == "delete":
        box = await runtime.get(args.target)
        if box is not None:
            with contextlib.suppress(Exception):
                await box.stop()
            await runtime.remove(args.target, force=True)
        return 0
    if args.action == "exec":
        return await boxlite_exec(args)
    if args.action == "terminal":
        return await boxlite_terminal(args)
    raise RuntimeError("unsupported BoxLite action")


def parser():
    root = argparse.ArgumentParser(prog="agentbox-runtime-driver")
    root.add_argument("driver", choices=("boxlite",))
    commands = root.add_subparsers(dest="action", required=True)
    commands.add_parser("probe")

    create = commands.add_parser("create")
    create.add_argument("target")
    create.add_argument("image")
    create.add_argument("--cpus", type=int, default=2)
    create.add_argument("--memory", type=int, default=4096)
    create.add_argument("--workdir", default="/workspace")
    create.add_argument("--network", default="restricted")

    for action in ("inspect", "start", "stop", "delete", "terminal"):
        command = commands.add_parser(action)
        command.add_argument("target")

    execute = commands.add_parser("exec")
    execute.add_argument("--stdin", action="store_true")
    execute.add_argument("--workdir")
    execute.add_argument("target")
    execute.add_argument("command", nargs=argparse.REMAINDER)
    return root


async def run(args):
    return await run_boxlite(args)


def main():
    args = parser().parse_args()
    if args.action == "exec":
        if args.command and args.command[0] == "--":
            args.command = args.command[1:]
        if not args.command:
            raise SystemExit("exec requires a command after --")
    try:
        raise SystemExit(asyncio.run(run(args)))
    except KeyboardInterrupt:
        raise SystemExit(130)
    except Exception as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1)


if __name__ == "__main__":
    main()
`
