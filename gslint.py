import os
import sublime
import sublime_plugin

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


class GsCompLintCommand(sublime_plugin.TextCommand):
    def run(self, edit: sublime.Edit) -> None:
        if gs.setting("comp_lint_enabled") is not True:
            return
        if self.view is None:
            return
        filename = self.view.file_name()
        if not filename:
            return
        filename = os.path.abspath(filename)
        mg9.acall("comp_lint", {"filename": filename}, self.callback)

    def cleanup(self, view: sublime.View) -> None:
        view.set_status(DOMAIN, "")
        view.erase_regions(DOMAIN)
        view.erase_regions(DOMAIN + "-zero")

    def highlight_view(self, view: sublime.View, reports: Dict[int, Report]) -> None:
        if view is None:
            return

        self.cleanup(view)
        if not reports:
            return

        sel = gs.sel(view).begin()
        row, _ = view.rowcol(sel)

        regions = []
        regions0 = []
        domain0 = DOMAIN + "-zero"
        for r in reports.values():
            line = view.line(view.text_point(r.row, 0))
            pos = line.begin() + r.col
            if pos >= line.end():
                pos = line.end()
            if pos == line.begin():
                regions0.append(sublime.Region(pos, pos))
            else:
                regions.append(sublime.Region(pos, pos))

        if regions:
            view.add_regions(
                DOMAIN, regions, "comment", "dot", sublime.DRAW_EMPTY_AS_OVERWRITE
            )
        else:
            view.erase_regions(DOMAIN)

        if regions0:
            view.add_regions(domain0, regions0, "comment", "dot", sublime.HIDDEN)
        else:
            view.erase_regions(domain0)

        msg = ""
        if len(reports) > 0:
            msg = "%s (%d)" % (DOMAIN, len(reports))
            r = reports.get(row)
            if r and r.msg:
                msg = "%s: %s" % (msg, r.msg)

        view.set_status(DOMAIN, msg)

    def highlight(self, reports: Dict[int, Report]) -> None:
        for view in [self.view] + self.view.clones() or []:
            if view is not None and view.is_loading() is False:
                self.highlight_view(view, reports)

    def callback(self, res: GoCompLintResponse, err: Optional[str]) -> None:
        if err:
            gs.notice(DOMAIN, err)
        if "filename" not in res:
            gs.notice(DOMAIN, "comp_lint: missing filename")
            return

        reports = {}

        top_level_error = res.get("top_level_error", "")
        cmd_error = res.get("cmd_error", "")
        if top_level_error:
            gs.notice(DOMAIN, top_level_error)
            reports[0] = Report(row=0, col=0, msg=top_level_error)
        elif cmd_error:
            gs.notice(DOMAIN, cmd_error)
            reports[0] = Report(row=0, col=0, msg=cmd_error)

        filename = res["filename"]
        errors = res.get("errors")
        if errors is not None:
            for rep in errors:
                if rep.get("file", "") != filename:
                    continue
                msg = rep.get("message", "")
                if not msg:
                    continue
                row = max(int(rep.get("row", 0)) - 1, 0)
                col = max(int(rep.get("col", 0)) - 1, 0)
                if row in reports:
                    reports[row].msg = "%s\n%s" % (reports[row].msg, msg)
                    reports[row].col = max(reports[row].col, col)
                else:
                    reports[row] = Report(row=row, col=col, msg=msg)

        sublime.set_timeout(lambda: self.highlight(reports), 0)
