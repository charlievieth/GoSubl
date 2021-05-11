import os
import sublime
import sublime_plugin
import threading

# WARN: we get an import error trying to use out typing package
# so just use the stdlib's package for now.
from typing import Dict
from typing import List
from typing import Optional
from typing import TypedDict

from gosubl import gs
from gosubl import mg9

DOMAIN = "GsLint"
CL_DOMAIN = "GsCompLint"

sem = threading.Semaphore()


class GoCompLintError(TypedDict):
    row: int
    col: int
    file: str
    message: str


class GoCompLintResponse(TypedDict):
    filename: str
    top_level_error: Optional[str]
    cmd_error: Optional[str]
    errors: Optional[List[GoCompLintError]]


class Report(object):
    __slots__ = "row", "col", "msg"

    def __init__(self, row: int, col: int, msg: str) -> None:
        self.row = row
        self.col = col
        self.msg = msg


class FileRef(object):
    __slots__ = "view", "src", "tm", "state", "reports"

    def __init__(self, view: sublime.View) -> None:
        self.view: sublime.View = view
        self.src = ""
        self.tm: float = 0.0
        self.state: int = 0
        self.reports: Dict[int, Report] = {}


file_refs: Dict[str, FileRef] = {}


def highlight(fr: FileRef) -> None:
    sel = gs.sel(fr.view).begin()
    row, _ = fr.view.rowcol(sel)

    if fr.state == 1:
        fr.state = 0
        cleanup(fr.view)

        regions = []
        regions0 = []
        domain0 = DOMAIN + "-zero"
        for r in fr.reports.values():
            line = fr.view.line(fr.view.text_point(r.row, 0))
            pos = line.begin() + r.col
            if pos >= line.end():
                pos = line.end()
            if pos == line.begin():
                regions0.append(sublime.Region(pos, pos))
            else:
                regions.append(sublime.Region(pos, pos))

        if regions:
            fr.view.add_regions(
                DOMAIN, regions, "comment", "dot", sublime.DRAW_EMPTY_AS_OVERWRITE
            )
        else:
            fr.view.erase_regions(DOMAIN)

        if regions0:
            fr.view.add_regions(domain0, regions0, "comment", "dot", sublime.HIDDEN)
        else:
            fr.view.erase_regions(domain0)

    msg = ""
    reps = fr.reports.copy()
    if len(reps) > 0:
        msg = "%s (%d)" % (DOMAIN, len(reps))
        r = reps.get(row)
        if r and r.msg:
            msg = "%s: %s" % (msg, r.msg)

    if fr.state != 0:
        msg = u"\u231B %s" % msg

    fr.view.set_status(DOMAIN, msg)


def cleanup(view: sublime.View) -> None:
    view.set_status(DOMAIN, "")
    view.erase_regions(DOMAIN)
    view.erase_regions(DOMAIN + "-zero")


def ref(fn: str, validate: bool = True) -> FileRef:
    with sem:
        if validate:
            for fn in list(file_refs.keys()):
                fr = file_refs[fn]
                if not fr.view.window() or fn != fr.view.file_name():
                    del file_refs[fn]
        return file_refs.get(fn)


def do_comp_lint_callback(res: GoCompLintResponse, err: Optional[str]) -> None:
    if err:
        gs.notice(DOMAIN, err)
    if "filename" not in res:
        gs.notice(DOMAIN, "comp_lint: missing filename")
        return

    filename = res["filename"]
    fileref = ref(filename, False)
    if not fileref:
        return

    reports = {}
    if res.get("top_level_error", None):
        gs.notice(DOMAIN, res["top_level_error"])
        reports[0] = Report(row=0, col=0, msg=res["top_level_error"])

    if "errors" in res:
        try:
            for rep in res.get("errors", []):
                if rep["file"] != filename:
                    continue
                row = int(rep["row"]) - 1
                col = int(rep["col"]) - 1
                if col < 0:
                    col = 0
                msg = rep["message"]
                if row in reports:
                    reports[row].msg = "%s\n%s" % (reports[row].msg, msg)
                    reports[row].col = max(reports[row].col, col)
                else:
                    reports[row] = Report(row=row, col=col, msg=msg)
        except:
            gs.notice(DOMAIN, gs.traceback())

    def cb() -> None:
        fileref.reports = reports
        fileref.state = 1
        highlight(fileref)

    sublime.set_timeout(cb, 0)


class GsCompLintCommand(sublime_plugin.TextCommand):
    def run(self, edit: sublime.Edit) -> None:
        if gs.setting("comp_lint_enabled") is not True:
            return

        fn = self.view.file_name()
        fn = os.path.abspath(fn)
        if fn:
            file_refs[fn] = FileRef(self.view)
            mg9.acall("comp_lint", {"filename": fn}, do_comp_lint_callback)
