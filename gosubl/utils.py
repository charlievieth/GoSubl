try:
    from collections.abc import MutableMapping
except ImportError:
    from collections import MutableMapping

from threading import Lock


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


class ThreadSafeDict(MutableMapping):
    """ThreadSafeDict provides a thread-safe dictionary"""

    def __init__(self, initialdata=None, /, **kwargs):
        self.mutex = Lock()
        self.data = {}
        if map is not None:
            self.update(map)
        if kwargs:
            self.update(kwargs)

    def __len__(self):
        with self.mutex:
            return len(self.data)

    def __getitem__(self, key):
        with self.mutex:
            return self.data.__getitem__(key)

    def __setitem__(self, key, item):
        with self.mutex:
            self.data[key] = item

    def __delitem__(self, key):
        with self.mutex:
            del self.data[key]

    def __iter__(self):
        raise NotImplementedError(
            "Iteration over this class is unlikely to be threadsafe."
        )

    # Modify __contains__ to work correctly when __missing__ is present
    def __contains__(self, key):
        with self.mutex:
            return key in self.data

    # Now, add the methods in dicts but not in MutableMapping
    def __repr__(self):
        with self.mutex:
            return repr(self.data)

    def __eq__(self, other):
        with self.mutex:
            if isinstance(other, ThreadSafeDict):
                with other.mutex:
                    return self.data == other.data
            else:
                return self.data == other

    def __or__(self, other):
        with self.mutex:
            if isinstance(other, ThreadSafeDict):
                with other.mutex:
                    return self.__class__(self.data | other.data)
            if isinstance(other, dict):
                return self.__class__(self.data | other)
        return NotImplemented

    def __ror__(self, other):
        with self.mutex:
            if isinstance(other, ThreadSafeDict):
                with other.mutex:
                    return self.__class__(other.data | self.data)
            if isinstance(other, dict):
                return self.__class__(other | self.data)
        return NotImplemented

    def __ior__(self, other):
        with self.mutex:
            if isinstance(other, ThreadSafeDict):
                with other.mutex:
                    self.data |= other.data
            else:
                self.data |= other
        return self

    def __copy__(self):
        with self.mutex:
            inst = self.__class__.__new__(self.__class__)
            inst.__dict__.update(self.__dict__)
            # Create a copy and avoid triggering descriptors
            inst.__dict__["data"] = self.__dict__["data"].copy()
            return inst

    def get(self, key, default=None):
        with self.mutex:
            return self.data.get(key, default)

    __marker = object()

    def pop(self, key, default=__marker):
        with self.mutex:
            if default is self.__marker:
                return self.data.pop(key)
            else:
                return self.data.pop(key, default)

    def clear(self):
        with self.mutex:
            self.data.clear()

    def popitem(self):
        with self.mutex:
            return self.data.popitem()

    def update(self, other=(), /, **kwds):
        with self.mutex:
            if isinstance(other, ThreadSafeDict):
                with other.mutex:
                    self.data.update(other.data)
            else:
                self.data.update(other)

    def setdefault(self, key, default=None):
        with self.mutex:
            return self.data.setdefault(key, default)

    # WARN: not performant
    def keys(self):
        with self.mutex:
            return list(self.data.keys())

    # WARN: not performant
    def items(self):
        with self.mutex:
            return list(self.data.items())

    # WARN: not performant
    def values(self):
        return list(self.data.values())

    def copy(self):
        with self.mutex:
            if self.__class__ is ThreadSafeDict:
                return ThreadSafeDict(self.data.copy())
            import copy

            data = self.data
            try:
                self.data = {}
                c = copy.copy(self)
            finally:
                self.data = data
            c.update(self)
            return c

    @classmethod
    def fromkeys(cls, iterable, value=None):
        d = cls()
        for key in iterable:
            d[key] = value
        return d
