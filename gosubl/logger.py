import gzip
import logging
import os
import queue
import sys
# import secrets
# import threading

from datetime import datetime
# from logging import handlers

from logging.handlers import QueueHandler
from logging.handlers import QueueListener
from logging.handlers import RotatingFileHandler

from pathlib import Path
from platform import system
from shutil import copyfileobj

from typing import List
from typing import Optional
from typing import IO
from typing import Union

from gosubl.gs import home_path


LOG_MAX_BYTES = 10 * 1024 * 1024
LOGFILE: Optional[IO] = None
_MARGO_LOGFILE: Optional[IO] = None


def RotateLogs(
    filename: Union[str, os.PathLike],
    max_bytes: int = LOG_MAX_BYTES,
    backup_count: int = 5,
) -> None:
    try:
        handler = RotatingFileHandler(
            filename=filename,
            maxBytes=LOG_MAX_BYTES,
            backupCount=5,
            encoding='utf-8'
        )
        if not os.path.exists(handler.baseFilename):
            os.makedirs(os.path.dirname(handler.baseFilename), exist_ok=True)
        elif handler.shouldRollover(logging.LogRecord(
            name='test record',
            level=logging.ERROR,
            pathname=__file__,
            lineno=123,
            msg='test message',
            args=None,
            exc_info=None,
        )):
            handler.doRollover()
    finally:
        handler.close()


class LogFile(RotatingFileHandler):
    def __init__(
        self,
        filename: str,
        maxBytes: int = LOG_MAX_BYTES,
        backupCount: int = 5,
    ):
        filename = os.path.abspath(filename)
        if not os.path.exists(filename):
            os.makedirs(os.path.dirname(filename), exist_ok=True)

        RotatingFileHandler.__init__(
            self,
            filename,
            maxBytes=maxBytes,
            backupCount=backupCount,
            encoding="utf-8",
        )
        if self.shouldRollover(self._fakeLogRecord()):
            self.doRollover()

    def _fakeLogRecord(self) -> logging.LogRecord:
        return logging.LogRecord(
            name='test record',
            level=logging.ERROR,
            pathname=__file__,
            lineno=123,
            msg='test message',
            args=None,
            exc_info=None,
        )


class MargoLogFile(LogFile):
    def __init__(
        self,
        maxBytes: int = LOG_MAX_BYTES,
        backupCount: int = 5,
    ):
        LogFile.__init__(
            self,
            filename=self.logname(),
            maxBytes=maxBytes,
            backupCount=backupCount,
        )

    @classmethod
    def logname(cls) -> str:
        if system() == "Darwin":
            return os.path.expanduser("~/Library/Logs/GoSublime/gosubl.log")
        else:
            return os.path.abspath(os.path.join(home_path(), "gosubl.log"))


def log_directory() -> str:
    if system() == "Darwin":
        logdir = os.path.expanduser("~/Library/Logs/GoSublime")
    else:
        logdir = os.path.join(home_path(), "logs")
    return os.path.abspath(logdir)


def margo_log_filename() -> str:
    if system() == "Darwin":
        logname = os.path.expanduser("~/Library/Logs/GoSublime/gosubl.log")
    else:
        logname = os.path.join(home_path(), "gosubl.log")
    return os.path.abspath(logname)


def open_margo_log_file(
    max_bytes: int = LOG_MAX_BYTES,
    backup_count: int = 5,
) -> IO:
    global _MARGO_LOGFILE
    if _MARGO_LOGFILE:
        return _MARGO_LOGFILE
    filename = margo_log_filename()
    if not os.path.exists(filename):
        os.makedirs(os.path.dirname(filename), exist_ok=True)
    try:
        RotateLogs(filename, max_bytes, backup_count)
    except Exception as e:
        # TODO: use DEVNULL as the log
        raise e
    _MARGO_LOGFILE = open(filename, "a+")
    return _MARGO_LOGFILE

    # _MARGO_LOGFILE = open
    # if not fp.exists():
    #     fp.parent.mkdir(parents=True, exist_ok=True)
    #     fp.touch()
    #
    # elif max_bytes > 0 and fp.stat().st_size >= max_bytes:
    #     ts = int(datetime.utcnow().timestamp())
    #     new = fp.parent / f"{ts}.{fp.name}.gz"
    #     if not new.exists():
    #         with open(fp, 'rb') as f_in:
    #             with gzip.open(new, 'xb') as f_out:
    #                 copyfileobj(f_in, f_out)
    #         fp.unlink()  # delete
    #         fp.touch()   # recreate
    #
    # if backup_count > 0:
    #     backups = sorted(fp.parent.glob("*.gz"))
    #     while backups and len(backups) > backup_count:
    #         backups.pop(0).unlink(missing_ok=True)
    #
    # return str(fp)


def init_log_file(
    max_bytes: int = LOG_MAX_BYTES,
    backup_count: int = 5,
) -> str:
    if system() == "Darwin":
        fp = Path("~/Library/Logs/GoSublime/margo.log").expanduser()
    else:
        fp = Path(home_path()) / "margo.log"

    if not fp.exists():
        fp.parent.mkdir(parents=True, exist_ok=True)
        fp.touch()

    elif max_bytes > 0 and fp.stat().st_size >= max_bytes:
        ts = int(datetime.utcnow().timestamp())
        new = fp.parent / f"{ts}.{fp.name}.gz"
        if not new.exists():
            with open(fp, 'rb') as f_in:
                with gzip.open(new, 'xb') as f_out:
                    copyfileobj(f_in, f_out)
            fp.unlink()  # delete
            fp.touch()   # recreate

    if backup_count > 0:
        backups = sorted(fp.parent.glob("*.gz"))
        while backups and len(backups) > backup_count:
            backups.pop(0).unlink(missing_ok=True)

    return str(fp)


def setup_queue_logger(
    log_directory: os.PathLike,
    level: int = logging.INFO,
    maxBytes: int = LOG_MAX_BYTES,
    backupCount: int = 5,
    stream_stderr: bool = True,
) -> QueueListener:
    if not os.path.exists(log_directory):
        os.makedirs(log_directory, exist_ok=True)

    handlers: List[logging.StreamHandler] = [
        RotatingFileHandler(
            filename=os.path.join(log_directory, 'gosubl.log'),
            maxBytes=maxBytes,
            backupCount=backupCount,
            encoding='utf-8'
        )
    ]
    if stream_stderr:
        handlers.append(logging.StreamHandler(sys.stderr))

    queue_listener = QueueListener(
        queue.Queue(-1),
        *handlers,
        respect_handler_level=True,
    )
    queue_handler = QueueHandler(queue_listener.queue)

    logging.basicConfig(
        level=level,
        # TODO (CEV): use this format and name loggers
        # format='%(asctime)s %(name)s: %(levelname)s %(message)s',
        format='%(asctime)s: %(filename)s:#%(lineno)d %(levelname)s: %(message)s',
        # format='%(asctime)s: %(levelname)-8s: %(message)s',
        handlers=[queue_handler],
    )

    queue_listener.start()
    return queue_listener
