from collections import OrderedDict
from threading import Lock
from threading import RLock
from time import sleep
from typing import Any
from typing import Iterator
from typing import Optional

import sublime


class Counter:
    """Counter provides a thread-safe counter."""
    __slots__ = "_lock", "_value"

    def __init__(self, value: int = 0) -> None:
        self._lock = Lock()
        self._value = value

    def value(self) -> int:
        with self._lock:
            return self._value

    def next(self) -> int:
        with self._lock:
            self._value += 1
            return self._value

    def __repr__(self) -> str:
        return f"Counter(value={self.value()})"


class LRUCache(OrderedDict):
    def __init__(self, maxsize: int = 128) -> None:
        self._maxsize = maxsize
        self._lock = RLock()
        super().__init__()

    @property
    def maxsize(self) -> int:
        return self._maxsize

    def __getitem__(self, key: Any) -> Any:
        with self._lock:
            value = OrderedDict.__getitem__(self, key)
            # popitem() deletes the item from it's ordered map before
            # caling dict.pop() so ignore the move_to_end() exception
            try:
                OrderedDict.move_to_end(self, key, last=False)
            except KeyError:
                pass
            return value

    def __setitem__(self, key: Any, value: Any) -> None:
        with self._lock:
            if len(self) >= self._maxsize and key not in self:
                OrderedDict.popitem(self, last=True)
            OrderedDict.__setitem__(self, key, value)
            OrderedDict.move_to_end(self, key, last=False)

    def __delitem__(self, key: Any) -> None:
        with self._lock:
            OrderedDict.__delitem__(self, key)

    def __contains__(self, key: Any) -> bool:
        with self._lock:
            return OrderedDict.__contains__(self, key)

    def __iter__(self) -> Iterator:
        with self._lock:
            return OrderedDict.__iter__(self)

    def __reversed__(self) -> Iterator:
        with self._lock:
            return OrderedDict.__reversed__(self)

    def clear(self) -> None:
        with self._lock:
            OrderedDict.clear(self)


def view_file_name(view: Optional[sublime.View]) -> str:
    if not view:
        return ""
    name = view.file_name()
    return name if name else ""


def view_is_loaded(view: Optional[sublime.View]) -> bool:
    if not view:
        return False
    i = 0
    loading = view.is_loading()
    while loading and i < 5:
        i += 1
        sleep(0.05)
        loading = view.is_loading()
    return not loading


def first_selection_region(view: sublime.View) -> Optional[sublime.Region]:
    try:
        return view.sel()[0]
    except IndexError:
        return None
