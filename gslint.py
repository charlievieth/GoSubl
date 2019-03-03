from gosubl import gs
from gosubl import mg9
import os
import sublime
import sublime_plugin
import threading
import time

DOMAIN = "GsLint"
CL_DOMAIN = "GsCompLint"


class FileRef(object):
    def __init__(self, view):
        self.view = view
        self.src = ""
        self.tm = 0.0
        self.state = 0
        self.reports = {}


class Report(object):
    def __init__(self, row, col, msg):
        self.row = row
        self.col = col
        self.msg = msg


class GsLintThread(threading.Thread):
    def __init__(self):
        threading.Thread.__init__(self)
        self.daemon = True
        self.sem = threading.Semaphore()
        self.s = set()
        self.q = gs.queue.Queue()

    def putq(self, fn):
        with self.sem:
            if fn in self.s:
                return False
            self.s.add(fn)
            self.q.put(fn)
            return True

    def popq(self):
        fn = self.q.get()
        with self.sem:
            self.s.discard(fn)
        return fn

    def run(self):
        while True:
            fn = self.popq()
            fr = ref(fn, False)
            if fr:
                reports = {}
                res, _ = mg9.bcall(
                    "lint",
                    {
                        "dir": (os.path.dirname(fn) if fn else ""),
                        "fn": fn,
                        "src": fr.src,
                        "filter": gs.setting("lint_filter", []),
                    },
                )
                res = gs.dval(res, {})
                for r in gs.dval(res.get("reports"), []):
                    if fn and fn != "<stdin>" and r.get("Fn") != fn:
                        continue

                    kind = r.get("Kind", "")
                    row = r.get("Row", 0)
                    col = r.get("Col", 0)
                    msg = r.get("Message", "")
                    msg = "%s: %s" % (kind, msg)
                    if row >= 0 and msg:
                        reports[row] = Report(row, col, msg)

                fr = ref(fn, False)
                if fr:
                    with sem:
                        fr.state = 1
                        fr.reports = reports
                        file_refs[fn] = fr


def highlight(fr):
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
    l = len(reps)
    if l > 0:
        msg = "%s (%d)" % (DOMAIN, l)
        r = reps.get(row)
        if r and r.msg:
            msg = "%s: %s" % (msg, r.msg)

    if fr.state != 0:
        msg = u"\u231B %s" % msg

    fr.view.set_status(DOMAIN, msg)


def cleanup(view):
    view.set_status(DOMAIN, "")
    view.erase_regions(DOMAIN)
    view.erase_regions(DOMAIN + "-zero")


def watch():
    global file_refs
    global th

    view = gs.active_valid_go_view()

    if view is not None and (
        view.file_name() and gs.setting("comp_lint_enabled") is True
    ):
        fn = view.file_name()
        fr = ref(fn)
        with sem:
            if fr:
                fr.view = view
                highlight(fr)
        sublime.set_timeout(watch, 150)  # Previously 500
        return

    if gs.setting("gslint_enabled") is not True:
        if view:
            with sem:
                for fn in file_refs:
                    fr = file_refs[fn]
                    cleanup(fr.view)
                file_refs = {}
        sublime.set_timeout(watch, 150)  # Previously 2000
        return

    if view and not view.is_loading():
        fn = view.file_name()
        fr = ref(fn)
        with sem:
            if fr:
                # always use the active view (e.g in split-panes)
                fr.view = view
                highlight(fr)
            else:
                fr = FileRef(view)

            file_refs[fn] = fr
            if fr.state == 0:
                src = view.substr(sublime.Region(0, view.size()))
                if src != fr.src:
                    fr.src = src
                    fr.tm = time.time()

                if fr.tm > 0.0:
                    timeout = int(gs.setting("gslint_timeout", 500))
                    delta = int((time.time() - fr.tm) * 1000.0)
                    if delta >= timeout:
                        fr.tm = 0.0
                        fr.state = -1
                        if not th:
                            th = GsLintThread()
                            th.start()
                        th.putq(fn)

    sublime.set_timeout(watch, 150)  # Previously 500


def ref(fn, validate=True):
    with sem:
        if validate:
            for fn in list(file_refs.keys()):
                fr = file_refs[fn]
                if not fr.view.window() or fn != fr.view.file_name():
                    del file_refs[fn]
        return file_refs.get(fn)


def do_comp_lint_callback(res, err):
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

    def cb():
        fileref.reports = reports
        fileref.state = 1
        highlight(fileref)

    sublime.set_timeout(cb, 0)


class GsCompLintCommand(sublime_plugin.TextCommand):
    def run(self, edit):
        if gs.setting("comp_lint_enabled") is not True:
            return

        fn = self.view.file_name()
        fn = os.path.abspath(fn)
        if fn:
            dirname = gs.basedir_or_cwd(fn)
            file_refs[fn] = FileRef(self.view)
            mg9.acall("comp_lint", {"filename": fn}, do_comp_lint_callback)


try:
    th
except:
    th = None
    sem = threading.Semaphore()
    file_refs = {}

    watch()
