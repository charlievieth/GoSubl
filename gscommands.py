from gosubl import gs
from gosubl import gspatch
from gosubl import mg9

import datetime
import os
import re

import sublime
import sublime_plugin

DOMAIN = "GoSublime"

GENERATED_CODE_RE = re.compile(
    r"// .*DO NOT EDIT\.?",
    re.MULTILINE,
)

PACKAGE_RE = re.compile(
    r"^package ",
    re.MULTILINE,
)

# TODO: make this configurable
FMT_IGNORED_EXTS = tuple([
    ".pb.go"
])


class GsCommentForwardCommand(sublime_plugin.TextCommand):
    def run(self, edit):
        self.view.run_command("toggle_comment", {"block": False})
        self.view.run_command("move", {"by": "lines", "forward": True})


class GsStartNextLineCommentCommand(sublime_plugin.TextCommand):
    def run(self, edit):
        self.view.run_command(
            "run_macro_file", {"file": "Packages/Default/Add Line.sublime-macro"}
        )
        self.view.run_command("toggle_comment", {"block": False})


class GsFmtCommand(sublime_plugin.TextCommand):
    def is_enabled(self):
        if gs.setting("fmt_enabled") is True:
            fn = self.view.file_name() or ""
            if self.is_ignored(fn):
                self.log_ignored("extension", fn)
                return False

            if fn.endswith(".go") or gs.is_go_source_view(self.view):
                return True
        return False

    def run(self, edit):
        vsize = self.view.size()
        src = self.view.substr(sublime.Region(0, vsize))
        if src.isspace():
            return

        # Check if the file is ignored by extension or is generated.
        file_name = self.view.file_name()
        if self.is_ignored(file_name):
            self.log_ignored("extension", file_name)
            return
        if self.is_generated(src):
            self.log_ignored("generated", file_name)
            return

        res, err = mg9.fmt(file_name, src)
        if err:
            gs.println(DOMAIN, "cannot fmt file. error: `%s'" % err)
            return

        if res.get("no_change", False):
            return

        src = res.get("src", "")
        if not src:
            gs.println(DOMAIN, "cannot fmt file. it appears to be empty")
            return

        _, err = gspatch.merge(self.view, vsize, src, edit)
        if err:
            msg = "PANIC: Cannot fmt file. Check your source for errors (and maybe undo any changes)."
            sublime.error_message("%s: %s: Merge failure: `%s'" % (DOMAIN, msg, err))

    @classmethod
    def is_generated(cls, src: str) -> bool:
        if src.startswith("package ") or "DO NOT EDIT" not in src:
            return False
        # Require the "DO NOT EDIT" comment to occur before the
        # package statement.
        m = PACKAGE_RE.search(src)
        if m and m.start() > 0:
            return GENERATED_CODE_RE.search(src, 0, m.start()) is not None
        return False

    @classmethod
    def is_ignored(cls, filename: str) -> bool:
        return filename is not None and filename.endswith(FMT_IGNORED_EXTS)

    @classmethod
    def log_ignored(cls, reason: str, file_name: str) -> None:
        msg = "fmt: ignoring file ({}): {}".format(
            reason, os.path.basename(file_name),
        )
        gs.println(DOMAIN, msg)
        gs.status_message(msg)


class GsFmtSaveCommand(sublime_plugin.TextCommand):
    def is_enabled(self):
        if gs.setting("fmt_enabled") is True:
            fn = self.view.file_name() or ""
            return fn.endswith(".go") or gs.is_go_source_view(self.view)
        return False

    def run(self, edit):
        self.view.run_command("gs_fmt")
        sublime.set_timeout(lambda: self.view.run_command("save"), 0)


class GsFmtPromptSaveAsCommand(sublime_plugin.TextCommand):
    def is_enabled(self):
        return gs.is_go_source_view(self.view)

    def run(self, edit):
        self.view.run_command("gs_fmt")
        sublime.set_timeout(lambda: self.view.run_command("prompt_save_as"), 0)


class GsGotoRowColCommand(sublime_plugin.TextCommand):
    # WARN (CEV): use the Jumper logic
    def run(self, edit, row, col=0):
        pt = self.view.text_point(row, col)
        r = sublime.Region(pt, pt)
        self.view.sel().clear()
        self.view.sel().add(r)
        self.view.show(pt)
        dmn = "gs.focus.%s:%s:%s" % (gs.view_fn(self.view), row, col)
        flags = sublime.DRAW_EMPTY_AS_OVERWRITE
        show = lambda: self.view.add_regions(dmn, [r], "comment", "bookmark", flags)
        hide = lambda: self.view.erase_regions(dmn)

        for i in range(3):
            m = 300
            s = i * m * 2
            h = s + m
            sublime.set_timeout(show, s)
            sublime.set_timeout(hide, h)


# TODO (CEV): this is worthless - remove it and the set_syntax stuff
class GsNewGoFileCommand(sublime_plugin.WindowCommand):
    def run(self):
        pkg_name = "main"
        view = gs.active_valid_go_view()
        try:
            basedir = gs.basedir_or_cwd(view and view.file_name())
            for fn in os.listdir(basedir):
                if fn.endswith(".go"):
                    name, _ = mg9.pkg_name(os.path.join(basedir, fn), "")
                    if name:
                        pkg_name = name
                        break
        except Exception:
            gs.error_traceback("GsNewGoFile")

        self.window.new_file().run_command(
            "gs_create_new_go_file", {"pkg_name": pkg_name, "file_name": "main.go"}
        )


# TODO (CEV): this is worthless - remove it and the set_syntax stuff
class GsCreateNewGoFileCommand(sublime_plugin.TextCommand):
    def run(self, edit, pkg_name, file_name):
        view = self.view
        view.set_name(file_name)
        view.set_syntax_file(gs.tm_path("go"))
        view.replace(edit, sublime.Region(0, view.size()), "package %s\n" % pkg_name)
        view.sel().clear()
        view.sel().add(view.find(pkg_name, 0, sublime.LITERAL))


class GsShowTasksCommand(sublime_plugin.WindowCommand):
    def run(self):
        ents = []
        now = datetime.datetime.now()
        m = {}
        try:
            tasks = gs.task_list()
            ents.insert(0, ["", "%d active task(s)" % len(tasks)])
            for tid, t in tasks:
                cancel_text = ""
                if t["cancel"]:
                    cancel_text = " (cancel task)"
                    m[len(ents)] = tid

                ents.append(
                    [
                        "#%s %s%s" % (tid, t["domain"], cancel_text),
                        t["message"],
                        "started: %s" % t["start"],
                        "elapsed: %s" % (now - t["start"]),
                    ]
                )
        except:
            ents = [["", "Failed to gather active tasks"]]

        def cb(i, _):
            gs.cancel_task(m.get(i, ""))

        gs.show_quick_panel(ents, cb)


class GsOpenHomePathCommand(sublime_plugin.WindowCommand):
    def run(self, fn):
        self.window.open_file(gs.home_path(fn))


class GsOpenDistPathCommand(sublime_plugin.WindowCommand):
    def run(self, fn):
        self.window.open_file(gs.dist_path(fn))


class GsSanityCheckCommand(sublime_plugin.WindowCommand):
    def run(self):
        s = "GoSublime Sanity Check\n\n%s" % "\n".join(
            mg9.sanity_check_sl(mg9.sanity_check({}, True))
        )
        gs.show_output("GoSublime", s)


class GsSetOutputPanelContentCommand(sublime_plugin.TextCommand):
    def run(self, edit, content, syntax_file, scroll_end, replace):
        panel = self.view
        panel.set_read_only(False)

        if replace:
            panel.replace(edit, sublime.Region(0, panel.size()), content)
        else:
            panel.insert(edit, panel.size(), content + "\n")

        panel.sel().clear()
        pst = panel.settings()
        pst.set("rulers", [])
        pst.set("fold_buttons", True)
        pst.set("fade_fold_buttons", False)
        pst.set("gutter", False)
        pst.set("line_numbers", False)

        if syntax_file:
            if syntax_file == "GsDoc":
                panel.set_syntax_file(gs.tm_path("doc"))
                panel.run_command("fold_by_level", {"level": 1})
            else:
                panel.set_syntax_file(syntax_file)

        panel.set_read_only(True)

        if scroll_end:
            panel.show(panel.size())


class GsInsertContentCommand(sublime_plugin.TextCommand):
    def run(self, edit, pos, content):
        pos = int(pos)  # un-fucking-believable
        self.view.insert(edit, pos, content)


class GsPatchImportsCommand(sublime_plugin.TextCommand):
    def run(self, edit, pos, content, added_path=""):
        pos = int(pos)  # un-fucking-believable
        view = self.view
        dirty, err = gspatch.merge(view, pos, content, edit)
        if err:
            gs.notice_undo(DOMAIN, err, view, dirty)
        elif dirty:
            k = "last_import_path.%s" % gs.view_fn(self.view)
            if added_path:
                gs.set_attr(k, added_path)
            else:
                gs.del_attr(k)
