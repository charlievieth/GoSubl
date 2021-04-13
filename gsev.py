import gstest
from gosubl import gs
from gosubl.typing import Any
from gosubl.typing import Dict
from gosubl.typing import List
from gosubl.typing import Optional

import sublime
import sublime_plugin

DOMAIN = "GsEV"


# TODO: rename - this handles running
class EV(sublime_plugin.EventListener):
    def on_pre_save(self, view: sublime.View) -> None:
        if view and gs.is_go_source_view(view):
            view.run_command("gs_fmt")
            if is_gohtml_view(view):
                sublime.set_timeout(lambda: do_set_gohtml_syntax(view), 0)

    def on_post_save(self, view: sublime.View) -> None:
        if view and gs.is_pkg_view(view):
            on_save = gs.setting("on_save")
            if on_save:
                sublime.set_timeout(lambda: do_post_save(view, on_save), 0)

    def on_activated(self, view: sublime.View) -> None:
        if view:
            do_sync_active_view(view)
            if gs.is_go_source_view(view):
                enforce_go_tab_size(view)
                if is_gohtml_view(view):
                    sublime.set_timeout(lambda: do_set_gohtml_syntax(view), 0)

    def on_load(self, view: sublime.View) -> None:
        if view and gs.is_go_source_view(view):
            enforce_go_tab_size(view)
            if is_gohtml_view(view):
                sublime.set_timeout(lambda: do_set_gohtml_syntax(view), 0)


class GsOnLeftClick(sublime_plugin.TextCommand):
    def run(self, edit: sublime.Edit) -> None:
        view = self.view
        if gs.is_go_source_view(view):
            if not gstest.handle_action(view, "left-click"):
                view.run_command("gs_doc", {"mode": "goto"})
        elif view.score_selector(gs.sel(view).begin(), "text.9o") > 0:
            win = view.window()
            if win:
                win.run_command("gs9o_open_selection")


class GsOnRightClick(sublime_plugin.TextCommand):
    def run(self, edit: sublime.Edit) -> None:
        view = self.view
        if gs.is_go_source_view(view):
            if not gstest.handle_action(view, "right-click"):
                view.run_command("gs_doc", {"mode": "hint"})


def enforce_go_tab_size(view: sublime.View) -> None:
    settings = view.settings()
    if settings:
        tab_size = settings.get("tab_size")
        if isinstance(tab_size, int) and tab_size != 4:
            settings.set("tab_size", 4)


def do_post_save(view: sublime.View, settings: List[Dict[str, Any]]) -> None:
    for c in settings:
        cmd = c.get("cmd", "")
        args = c.get("args", {})
        msg = "running on_save command %s" % cmd
        tid = gs.begin(DOMAIN, msg, set_status=False)
        try:
            view.run_command(cmd, args)
        except Exception as ex:
            gs.notice(DOMAIN, "Error %s" % ex)
        finally:
            gs.end(tid)


def do_sync_active_view(view: sublime.View) -> None:
    fn = view.file_name() or ""
    gs.set_attr("active_fn", fn)  # TODO: this is dumb - remove
    if fn and fn.endswith(".go"):
        gs.set_attr("last_active_go_fn", fn)


def is_gohtml_view(view: sublime.View) -> bool:
    exts: Optional[List[str]] = gs.setting("gohtml_extensions")
    if exts:
        fn = view.file_name() or ""
        return fn.endswith(tuple(exts))
    else:
        return False


def do_set_gohtml_syntax(view: sublime.View) -> None:
    if is_gohtml_view(view):
        view.set_syntax_file(gs.tm_path("gohtml"))
