import asyncio
import json
import sys

from asyncio.exceptions import LimitOverrunError
from asyncio.exceptions import IncompleteReadError
from typing import Callable
from typing import Optional
from typing import Union


async def read_output(
    stdout: Optional[asyncio.StreamReader],
    callback: Callable[[Union[str, bytes, bytearray]], None] = print,
) -> None:

    if stdout is None:
        raise ValueError('stdout is None')

    if callback is None:
        raise ValueError('callback is None')

    scratch: Optional[bytearray] = None
    while True:
        try:
            buf = await stdout.readuntil()
            if buf:
                if scratch:
                    scratch += buf
                    callback(scratch)
                    del scratch[:]
                else:
                    callback(buf)
            else:
                break
        except LimitOverrunError as e:
            buf = await stdout.read(e.consumed)
            if scratch is None:
                scratch = bytearray(buf)
            else:
                scratch += buf
        except IncompleteReadError as e:
            if e.partial:
                if scratch:
                    scratch += e.partial
                    callback(scratch)
                    del scratch[:]
                else:
                    callback(e.partial)
            if stdout.at_eof():
                break


async def write_stdin(stdin: asyncio.StreamWriter) -> None:
    try:
        for i in range(10):
            stdin.write(f"line: {i}\n".encode())
            await asyncio.sleep(0.1)
        await stdin.drain()
    finally:
        if stdin.can_write_eof():
            stdin.write_eof()
        if not stdin.is_closing():
            stdin.close()


def noop_callback(s: str) -> None:
    pass


async def run() -> None:
    proc = await asyncio.create_subprocess_exec(
        "cat", "/Users/cvieth/Projects/python/repl/repl-18/compile_commands.json",
        # stdin=asyncio.subprocess.PIPE,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )

    await asyncio.gather(
        read_output(proc.stderr),
        read_output(proc.stdout),
        # read_output_array(proc.stderr, callback=noop_callback),
        # read_output_array(proc.stdout, callback=noop_callback),

        # write_stdin(proc.stdin),
        # abort_stdin(proc.stdin),
    )
