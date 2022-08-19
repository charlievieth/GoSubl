import sublime
import sublime_plugin

from typing import Dict
from typing import Optional

from gosubl import gs
from gosubl import mg9


try:
    # isascii() was added in 3.7 and ST4 uses 3.8
    "".isascii()
    HAS_ISASCII = True
except AttributeError:
    HAS_ISASCII = False


def first_selection_region(view: sublime.View) -> Optional[sublime.Region]:
    try:
        return view.sel()[0]
    except IndexError:
        return None


def get_position(view: sublime.View, event: Optional[dict] = None, point: Optional[int] = None) -> Optional[int]:
    if isinstance(point, int):
        return point
    if event:
        x, y = event.get("x"), event.get("y")
        if x is not None and y is not None:
            return view.window_to_text((x, y))
    try:
        return view.sel()[0].begin()
    except IndexError:
        return None


class GsRenameInputHandler(sublime_plugin.TextInputHandler):
    def want_event(self) -> bool:
        return False

    def __init__(self, view: sublime.View, placeholder: str) -> None:
        self.view = view
        self._placeholder = placeholder

    def name(self) -> str:
        return "new_name"

    def placeholder(self) -> str:
        return self._placeholder

    def initial_text(self) -> str:
        return self.placeholder()

    def validate(self, name: str) -> bool:
        return len(name) > 0


class GsRenameCommand(sublime_plugin.TextCommand):
    def is_enabled(self):
        return gs.is_go_source_view(self.view)

    def input(self, args: dict) -> Optional[sublime_plugin.TextInputHandler]:
        if "new_name" not in args:
            placeholder = args.get("placeholder", "")
            if not placeholder:
                point = args.get("point")
                # guess the symbol name
                if not isinstance(point, int):
                    region = first_selection_region(self.view)
                    if region is None:
                        return None
                    point = region.b
                placeholder = self.view.substr(self.view.word(point))
            return GsRenameInputHandler(self.view, placeholder)
        else:
            return None

    def run(
        self,
        edit: sublime.Edit,
        new_name: str = "",
        placeholder: str = "",
        position: Optional[int] = None,
        event: Optional[dict] = None,
        point: Optional[int] = None
    ) -> None:
        if not self.is_enabled():
            return

        if position is None:
            tmp_pos = get_position(self.view, event, point)
            if tmp_pos is None:
                return
            pos = tmp_pos
            if new_name:
                return self._do_rename(pos, new_name)
            else:
                # trigger InputHandler manually
                raise TypeError("required positional argument")
        else:
            if new_name:
                return self._do_rename(position, new_name)
            else:
                # trigger InputHandler manually
                raise TypeError("required positional argument")

    def _byte_offset(self, position: int) -> int:
        src = self.view.substr(sublime.Region(0, self.view.size()))
        if HAS_ISASCII and src.isascii():
            return position
        else:
            return len(src[:position].encode("utf-8"))

    def _do_rename(self, position: int, new_name: str) -> None:
        mg9.rename(
            self.view.file_name(), new_name, position, self._callback,
        )

    # TODO: fixup type annotations or remove
    def _callback(
        self,
        response: Dict[str, int],
        err: Optional[str],
    ):
        if err:
            gs.show_output(
                "GsRename-output", "// Error: %s" % err, False, "GsRename",
            )
