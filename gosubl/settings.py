from copy import deepcopy
from threading import Lock

from typing import Any
from typing import Dict
from typing import Iterator
from typing import Mapping

import sublime


class Settings(Mapping[str, Any]):
    _registered_on_change = False
    _settings: sublime.Settings

    def __init__(self, base_name: str):
        self.base_name = base_name
        self._cache: Dict[str, Any] = {}
        self._lock = Lock()
        self._load_settings()

    def _load_settings(self) -> None:
        self._settings = sublime.load_settings(
            self.base_name,
        )
        if self._settings:
            with self._lock:
                if not self._registered_on_change:
                    self._settings.add_on_change(
                        self.base_name,
                        self._on_change,
                    )
                    self._registered_on_change = True
                if self._cache:
                    self._cache.clear()

    def _on_change(self) -> None:
        self._load_settings()

    @property
    def settings(self) -> sublime.Settings:
        return self._settings

    @property
    def cache(self) -> Dict[str, Any]:
        with self._lock:
            return deepcopy(self._cache)

    def has(self, key: str) -> bool:
        with self._lock:
            return key in self._cache or self._settings.has(key)

    def get(self, key: str, default: Any = None) -> Any:
        with self._lock:
            if key in self._cache:
                return self._cache[key]
            elif self._settings.has(key):
                val = self._settings.get(key)
                self._cache[key] = val
                return val
            else:
                return default

    def set(self, key: str, value: Any, save_settings: bool = False) -> None:
        with self._lock:
            self._settings.set(key, value)
            self._cache[key] = value
            if save_settings:
                self.save_settings()

    def erase(self, key: str, save_settings: bool = False) -> None:
        with self._lock:
            self._settings.erase(key)
            if key in self._cache:
                del self._cache[key]
            if save_settings:
                self.save_settings()

    def save_settings(self) -> None:
        sublime.save_settings(self.base_name)

    def __getitem__(self, key: str) -> Any:
        with self._lock:
            return self._cache[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.set(key, value)

    def __delitem__(self, key: str) -> None:
        with self._lock:
            self._settings.erase(key)
            del self._cache[key]

    def __len__(self) -> int:
        return len(self._cache)

    def __iter__(self) -> Iterator:
        return self._cache.__iter__()

    def __contains__(self, key: object) -> bool:
        if isinstance(key, str):
            return self.has(key)
        else:
            raise TypeError("invalid key type: {}".format(type(key)))


_all_lock = Lock()
_all_settings: Dict[str, Settings] = {}


def load_settings(base_name: str) -> Settings:
    with _all_lock:
        if base_name in _all_settings:
            return _all_settings[base_name]
        else:
            settings = Settings(base_name)
            _all_settings[base_name] = settings
            return settings
