from gosubl import gs
from gosubl import mg9
import os
import sublime
import sublime_plugin
import threading

DOMAIN = 'GsLint'

lint_semaphore = threading.Semaphore()
file_refs = {}


class FileRef(object):
    def __init__(self, view):
        self.view = view
        self.src = ''
        self.tm = 0.0
        self.state = 0
        self.reports = {}


class Report(object):
    def __init__(self, row, col, msg):
        self.row = row
        self.col = col
        self.msg = msg


def cleanup(view):
    view.set_status(DOMAIN, '')
    view.erase_regions(DOMAIN)
    view.erase_regions(DOMAIN+'-zero')


def highlight(fr):
    sel = gs.sel(fr.view).begin()
    row, _ = fr.view.rowcol(sel)

    if fr.state == 1:
        fr.state = 0
        cleanup(fr.view)

        regions = []
        regions0 = []
        domain0 = DOMAIN+'-zero'
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
            fr.view.add_regions(DOMAIN, regions, 'comment', 'dot', sublime.DRAW_EMPTY_AS_OVERWRITE)
        else:
            fr.view.erase_regions(DOMAIN)

        if regions0:
            fr.view.add_regions(domain0, regions0, 'comment', 'dot', sublime.HIDDEN)
        else:
            fr.view.erase_regions(domain0)

    msg = ''
    reps = fr.reports.copy()
    l = len(reps)
    if l > 0:
        msg = '%s (%d)' % (DOMAIN, l)
        r = reps.get(row)
        if r and r.msg:
            msg = '%s: %s' % (msg, r.msg)

    if fr.state != 0:
        msg = u'\u231B %s' % msg

    fr.view.set_status(DOMAIN, msg)


def ref(fn, validate=True):
    with lint_semaphore:
        if validate:
            for fn in list(file_refs.keys()):
                fr = file_refs[fn]
                if not fr.view.window() or fn != fr.view.file_name():
                    del file_refs[fn]
        return file_refs.get(fn)


def delref(fn):
    with lint_semaphore:
        if fn in file_refs:
            del file_refs[fn]


def do_comp_lint(dirname, filename):
    fileref = ref(filename, False)
    if not fileref:
        return

    def callback(lint_reports, err):
        if err:
            gs.notice(DOMAIN, err)
            return
        reports = {}
        for rep in lint_reports:
            row = int(rep['row'])
            if row >= 0 and rep['msg']:
                col = int(rep['col'])
                msg = 'go: {}'.format(rep['msg'])
                if row in reports:
                    reports[row].msg = '{}\n{}'.format(reports[row].msg, msg)
                    reports[row].col = max(reports[row].col, col)
                else:
                    reports[row] = Report(row, col, msg)
        fileref.reports = reports
        fileref.state = 1
        highlight(fileref)

    req = {
        'filename': gs.apath(filename, dirname),
        'dirname': dirname,
        'env': {},  # TODO (CEV): use or remove
    }
    sublime.set_timeout(lambda: mg9.acall('complint', req, callback), 0)


class GsCompLintCommand(sublime_plugin.TextCommand):
    def run(self, edit):
        if gs.setting('comp_lint_enabled') is not True:
            return

        filename = self.view.file_name()
        filename = os.path.abspath(filename)
        if filename:
            dirname = gs.basedir_or_cwd(filename)
            file_refs[filename] = FileRef(self.view)
            do_comp_lint(dirname, filename)
