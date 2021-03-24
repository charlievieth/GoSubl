import re
import time
from os import path

import sublime
import sublime_plugin

from .explore_panel import ExplorerPanel

from gosubl.typing import Dict
from gosubl.typing import List
from gosubl.typing import Union
from gosubl.typing import Optional

from gosubl import gs
from gosubl import gsq
from gosubl import mg9

# history_list: is used to set the jump history
from Default.history_list import get_jump_history_for_view

DOMAIN = "GsDoc"

GOOS_PAT = re.compile(r"_(%s)" % "|".join(gs.GOOSES))
GOARCH_PAT = re.compile(r"_(%s)" % "|".join(gs.GOARCHES))

EXT_EXCLUDE = frozenset(
    [
        "1",
        "2",
        "3",
        "7z",
        "a",
        "bak",
        "bin",
        "cache",
        "com",
        "cpu",
        "db",
        "dll",
        "dynlib",
        "exe",
        "gif",
        "gz",
        "jpeg",
        "jpg",
        "lib",
        "mem",
        "o",
        "old",
        "out",
        "png",
        "pprof",
        "prof",
        "pyc",
        "pyo",
        "rar",
        "so",
        "swap",
        "tar",
        "tgz",
        "zip",
    ]
)


##############################################################
# TODO: use the LSP Point/Range style of referring to locations
# TODO: move all of these to a separate file
# TODO: add type annotations

# def get_position(view: sublime.View, event: dict = None) -> int:
#     if event:
#         return view.window_to_text((event["x"], event["y"]))
#     else:
#         return view.sel()[0].begin()


def view_is_loaded(view) -> bool:
    i = 0
    loading = view.is_loading()
    while loading and i < 5:
        if i < 5:
            i += 1
            time.sleep(0.05)
            loading = view.is_loading()
    return loading


class GsDocCommand(sublime_plugin.TextCommand):
    def is_enabled(self):
        return gs.is_go_source_view(self.view)

    def show_output(self, s):
        gs.show_output(DOMAIN + "-output", s, False, "GsDoc")

    @classmethod
    def toggle_indicator(cls, view, line: int, column: int,) -> None:
        if line > 1:
            line = line - 1
        if column > 1:
            column = column - 1
        pt = view.text_point(line, column)
        region_name = 'gosubl.indicator.{}.{}'.format(
            view.id(), line
        )
        gs.println('creating region: {}'.format(region_name))

        for i in range(3):
            delta = 300 * i * 2
            sublime.set_timeout(lambda: view.add_regions(
                region_name,
                [sublime.Region(pt, pt)],
                'comment',
                'bookmark',
                sublime.DRAW_EMPTY_AS_OVERWRITE
            ), delta)
            sublime.set_timeout(
                lambda: view.erase_regions(region_name),
                delta + 300
            )
        pass

    def jump(
        self,
        file_name: str,
        line: int,
        column: int,
        transient: bool = False,
    ) -> None:
        """Toggle mark indicator to focus cursor
        """

        position = "{}:{}:{}".format(file_name, line, column)
        get_jump_history_for_view(self.view).push_selection(self.view)
        gs.println("opening {}".format(position))
        # self.view.window().open_file(position, sublime.ENCODED_POSITION)
        view = self.view.window().open_file(position, sublime.ENCODED_POSITION)

        if not transient:
            # Spin to see if the view has loaded before using a callback
            if view_is_loaded(view):
                self.toggle_indicator(view, line, column)
            else:
                sublime.set_timeout_async(
                    lambda: self.toggle_indicator(view, line, column),
                    300,
                )

    def goto_callback(self, docs, err):
        if err:
            self.show_output("// Error: %s" % err)
        elif len(docs) and "fn" in docs[0]:
            d = docs[0]

            fn = d.get("fn", "")
            row = d.get("row", 0) + 1
            col = d.get("col", 0) + 1
            if fn:
                self.jump(fn, row, col)
            return
        else:
            self.show_output("%s: cannot find definition" % DOMAIN)

    def run(self, _, mode=""):
        view = self.view
        if (not gs.is_go_source_view(view)) or (mode not in ["goto", "hint"]):
            return

        pt = gs.sel(view).begin()
        src = view.substr(sublime.Region(0, view.size()))
        # WARN (CEV): should this be done here or should
        # we do it in the Go code instead?
        pt = len(src[:pt].encode("utf-8"))

        if mode == "goto":
            callback = self.goto_callback
        elif mode == "hint":
            # WARN: remove hint support
            self.show_output("// Error: hint not supported")
        mg9.doc(view.file_name(), src, pt, callback)


# WARN (CEV): maybe move this and any new code to a new file
class SourceLocation(object):
    def __init__(
        self,
        filename: str,
        line: int,
        col_start: int,
        col_end: int,
    ) -> None:
        self.filename = filename
        self._basename = None  # type: Optional[str]
        self.line = int(line)
        self.col_start = int(col_start)
        self.col_end = int(col_end)

    @classmethod
    def from_margo(cls, loc: dict) -> "SourceLocation":
        return SourceLocation(
            loc["filename"],
            loc["line"],
            loc["col_start"],
            loc["col_end"],
        )

    @property
    def basename(self) -> str:
        if not self._basename:
            self._basename = path.basename(self.filename)
        return self._basename

    def __repr__(self) -> str:
        return "SourceLocation(filename={}, line={}, col_start={}, col_end={})".format(
            self.filename, self.line, self.col_start, self.col_end,
        )

    def position(self) -> str:
        return "{}:{}:{}".format(self.filename, self.line, self.col_start)

    def location(self) -> str:
        return 'File: {} Line: {} Column: {}'.format(
            self.filename, self.line, self.col_start,
        )

    def usage(self) -> Dict[str, str]:
        return {
            'title': '{}:{}:{}'.format(self.basename, self.line, self.col_start),
            'location': self.location(),
            'position': self.position(),
        }


# TODO: move the location of this (only here cuz split view)
# TODO: preview references (see Anaconda package)
class GsReferencesCommand(sublime_plugin.TextCommand):
    def is_enabled(self, event: dict = None) -> bool:
        return gs.is_go_source_view(self.view)

    # WARN: remove
    def show_output(self, s: str) -> None:
        gs.show_output(DOMAIN + "-output", s, False, "GsDoc")

    # TODO: should move this to a shared location
    def jump(
        self,
        file_name: str,
        line: int,
        column: int,
        transient: bool = False,
    ) -> None:
        """Toggle mark indicator to focus cursor
        """

        position = "{}:{}:{}".format(file_name, line, column)
        get_jump_history_for_view(self.view).push_selection(self.view)
        gs.println("opening {}".format(position))
        self.view.window().open_file(position, sublime.ENCODED_POSITION)

        # TODO: implement this ???
        if not transient:
            pass
            # self._toggle_indicator(file_name, line, column)

    # TODO: fixup type annotations or remove
    def handle_response(
        self,
        locations: List[Dict[str, Union[str, int]]],
        err: Optional[str],
    ):
        if err:
            self.show_output("// Error: %s" % err)
        elif not locations:
            self.show_output("%s: cannot find references" % DOMAIN)
        else:
            # TODO: add information about the type in the 'title'

            usages = [SourceLocation.from_margo(x).usage() for x in locations]
            ExplorerPanel(self.view, usages).show([], True)

    def run(self, edit: sublime.Edit, event: dict = None) -> None:
        view = self.view
        if not gs.is_go_source_view(view):
            return

        pt = gs.sel(view).begin()
        src = view.substr(sublime.Region(0, view.size()))
        # WARN (CEV): do we want to do this here or in the Go code ???
        offset = len(src[:pt].encode("utf-8"))

        mg9.references(view.file_name(), None, offset, self.handle_response)
        pass


class GsBrowseDeclarationsCommand(sublime_plugin.WindowCommand):
    def run(self, dir=""):
        if dir == ".":
            self.present_current()
        elif dir:
            self.present("", "", dir)
        else:

            def f(res, err):
                if err:
                    gs.notice(DOMAIN, err)
                    return

                ents, m = handle_pkgdirs_res(res)
                if ents:
                    ents.insert(0, "Current Package")

                    def cb(i, win):
                        if i == 0:
                            self.present_current()
                        elif i >= 1:
                            self.present("", "", path.dirname(m[ents[i]]))

                    gs.show_quick_panel(ents, cb)
                else:
                    gs.show_quick_panel([["", "No source directories found"]])

            mg9.pkg_dirs(f)

    def present_current(self):
        pkg_dir = ""
        view = gs.active_valid_go_view(win=self.window, strict=False)
        if view:
            if view.file_name():
                pkg_dir = path.dirname(view.file_name())
            vfn = gs.view_fn(view)
            src = gs.view_src(view)
        else:
            vfn = ""
            src = ""
        self.present(vfn, src, pkg_dir)

    def present(self, vfn, src, pkg_dir):
        win = self.window
        if win is None:
            return

        def f(res, err):
            if err:
                gs.notify(DOMAIN, err)
                return

            decls = res.get("file_decls", [])
            for d in res.get("pkg_decls", []):
                if not vfn or d["fn"] != vfn:
                    decls.append(d)

            for d in decls:
                dname = d["repr"] or d["name"]
                trailer = []
                trailer.extend(GOOS_PAT.findall(d["fn"]))
                trailer.extend(GOARCH_PAT.findall(d["fn"]))
                if trailer:
                    trailer = " (%s)" % ", ".join(trailer)
                else:
                    trailer = ""
                d["ent"] = "%s %s%s" % (d["kind"], dname, trailer)

            ents = []
            decls.sort(key=lambda d: d["ent"])
            for d in decls:
                ents.append(d["ent"])

            def cb(i, win):
                if i >= 0:
                    d = decls[i]
                    gs.focus(d["fn"], d["row"], d["col"], win)

            if ents:
                gs.show_quick_panel(ents, cb)
            else:
                gs.show_quick_panel([["", "No declarations found"]])

        mg9.declarations(vfn, src, pkg_dir, f)


def handle_pkgdirs_res(res):
    m = {}
    for root, dirs in res.items():
        for dir, fn in dirs.items():
            if not m.get(dir):
                m[dir] = fn
    ents = list(m.keys())
    ents.sort(key=lambda a: a.lower())
    return (ents, m)


class GsBrowsePackagesCommand(sublime_plugin.WindowCommand):
    def run(self):
        def f(res, err):
            if err:
                gs.notice(DOMAIN, err)
                return

            ents, m = handle_pkgdirs_res(res)
            if ents:

                def cb(i, win):
                    if i >= 0:
                        dirname = gs.basedir_or_cwd(m[ents[i]])
                        win.run_command("gs_browse_files", {"dir": dirname})

                gs.show_quick_panel(ents, cb)
            else:
                gs.show_quick_panel([["", "No source directories found"]])

        mg9.pkg_dirs(f)


def ext_filter(pathname, basename, ext):
    if not ext:
        return basename == "makefile"
    if ext in EXT_EXCLUDE:
        return False
    if ext.endswith("~"):
        return False
    return True


def show_pkgfiles(dirname):
    ents = []
    m = {}

    try:
        dirname = path.abspath(dirname)
        for fn in gs.list_dir_tree(
            dirname, ext_filter, gs.setting("fn_exclude_prefixes", [])
        ):
            name = path.relpath(fn, dirname).replace("\\", "/")
            m[name] = fn
            ents.append(name)
    except Exception as ex:
        gs.notice(DOMAIN, "Error: %s" % ex)

    if ents:
        ents.sort(key=lambda a: a.lower())

        try:
            s = " ../  ( current: %s )" % dirname
            m[s] = path.join(dirname, "..")
            ents.insert(0, s)
        except Exception:
            pass

        def cb(i, win):
            if i >= 0:
                fn = m[ents[i]]
                if path.isdir(fn):
                    win.run_command("gs_browse_files", {"dir": fn})
                else:
                    gs.focus(fn, 0, 0, win)

        gs.show_quick_panel(ents, cb)
    else:
        gs.show_quick_panel([["", "No files found"]])


class GsBrowseFilesCommand(sublime_plugin.WindowCommand):
    def run(self, dir=""):
        if not dir:
            view = self.window.active_view()
            dir = gs.basedir_or_cwd(view.file_name() if view is not None else None)
        gsq.dispatch(
            "*", lambda: show_pkgfiles(dir), "scanning directory for package files"
        )
