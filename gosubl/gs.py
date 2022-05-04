import copy
import datetime
import gzip
import json
import os
import queue
import re
import string
import subprocess
import sys
import threading
import traceback as tbck

from locale import getpreferredencoding
from pathlib import Path
from platform import system
from shutil import copyfileobj
from subprocess import Popen, PIPE

from gosubl import about
from gosubl.typing import Any
from gosubl.typing import Callable
from gosubl.typing import Dict
from gosubl.typing import List
from gosubl.typing import Optional
from gosubl.typing import Tuple
from gosubl.typing import TypeVar
from gosubl.typing import Union
from gosubl.typing import IO
from gosubl.utils import Counter

import sublime

# TODO: remove python2 support
PY3K = sys.version_info[0] == 3

# TRY_ENCODINGS are the encodings used to encode strings by ustr()
TRY_ENCODINGS = frozenset([
    "utf-8",
    getpreferredencoding().lower(),
])

# Windows
if os.name == 'nt':
    try:
        STARTUP_INFO = subprocess.STARTUPINFO()  # noqa
        STARTUP_INFO.dwFlags |= subprocess.STARTF_USESHOWWINDOW  # noqa
        STARTUP_INFO.wShowWindow = subprocess.SW_HIDE  # noqa
    except AttributeError:
        STARTUP_INFO = None
else:
    STARTUP_INFO = None

NAME = "GoSubl"
MAX_LOG_BYTES = 5 * 1024 * 1024
LOGFILE: IO = None

mg9_send_q = queue.Queue()
mg9_recv_q = queue.Queue()

_attr_lck = threading.Lock()
_attr = {}

_checked_lck = threading.Lock()
_checked = {}


# TODO: _env_lck appears to not be used.
_env_lck = threading.Lock()

environ9 = {}
_default_settings = {
    "margo_oom": 0,
    "_debug": False,
    "env": {},
    "gscomplete_enabled": False,
    "complete_builtins": False,
    "autocomplete_builtins": False,
    "fmt_enabled": False,
    "fmt_tab_indent": True,
    "fmt_tab_width": 8,
    "fmt_cmd": [],
    "gslint_enabled": False,
    "comp_lint_enabled": False,
    "comp_lint_commands": [],
    "gslint_timeout": 0,
    "calltips": True,
    "autocomplete_snippets": False,
    "autocomplete_tests": False,
    "autocomplete_closures": False,
    "autocomplete_filter_name": "",
    "autocomplete_suggest_imports": False,
    "on_save": [],
    "shell": [],
    "default_snippets": [],
    "snippets": [],
    "fn_exclude_prefixes": [".", "_"],
    "autosave": True,
    "build_command": [],
    "lint_filter": [],
    "lint_enbled": True,
    "linters": [],
    "9o_instance": "",
    "9o_color_scheme": "",
    "9o_settings": {},
    "9o_aliases": {},
    "9o_show_end": False,
    "gohtml_extensions": [],
    "autoinst": False,
    "use_gs_gopath": False,
    "use_named_imports": False,
    "installsuffix": "",
    "ipc_timeout": 1,
}
_settings: Dict[str, Any] = copy.copy(_default_settings)


CLASS_PREFIXES = {
    "const": u"\u0196",
    "func": u"\u0192",
    "type": u"\u0288",
    "var": u"\u03BD",
    "package": u"package \u03C1",
}

NAME_PREFIXES = {"interface": u"\u00A1"}

# TODO: Update via margo
GOARCHES = ["386", "amd64", "arm"]

# TODO: Update via margo
GOOSES = ["darwin", "freebsd", "linux", "netbsd", "openbsd", "plan9", "windows", "unix"]

IGNORED_SCOPES = frozenset(
    [
        "string.quoted.double.go",
        "string.quoted.single.go",
        "string.quoted.raw.go",
        "comment.line.double-slash.go",
        "comment.block.go",
        # gs-next
        "comment.block.go",
        "comment.line.double-slash.go",
        "string.quoted.double.go",
        "string.quoted.raw.go",
        "constant.other.rune.go",
    ]
)

VFN_ID_PAT = re.compile(r"^(?:gs\.)?view://(\d+)(.*?)$", re.IGNORECASE)
ROWCOL_PAT = re.compile(r"^[:]*(\d+)(?:[:](\d+))?[:]*$")

USER_DIR = os.path.expanduser("~").replace("\\", "/").rstrip("/")

# WARN: remove if not used
# Generic type for generic functions
T = TypeVar("T")
# WARN: remove if not used

TaskT = Dict[str, Union[str, datetime.datetime, Callable[[], None]]]


# WARN WARN WARN
_task_counter = Counter()


class Task:
    __slots__ = "domain", "message", "cancel", "start"

    def __init__(
        self,
        domain: str,
        message: str,
        cancel: Callable[[], None],
    ) -> None:
        self.domain = domain
        self.message = message
        self.cancel = cancel
        self.start = datetime.datetime.now()


def simple_fn(name: str) -> str:
    if "\\" in name:
        name = name.replace("\\", "/")
    if name.startswith("~/"):
        name = USER_DIR + name[1:]
    return name.rstrip("/")


def getwd() -> str:
    return os.getcwd()


def apath(fn: str, cwd: Optional[str] = None) -> str:
    if not os.path.isabs(fn):
        if not cwd:
            cwd = getwd()
        fn = os.path.join(cwd, fn)
    return os.path.normcase(os.path.normpath(fn))


def basedir_or_cwd(name: Optional[str]) -> str:
    if name and not name.startswith("gs.view://"):
        return os.path.dirname(name)
    else:
        return os.getcwd()


def popen(
    args: List[str],
    stdout: int = PIPE,
    stderr: int = PIPE,
    shell: bool = False,
    environ: Dict[str, str] = {},
    cwd: Optional[str] = None,
    bufsize: int = 0,
) -> Popen:
    ev = env()
    for k, v in environ.items():
        ev[astr(k)] = astr(v)

    try:
        setsid: Optional[Callable[[], None]] = os.setsid
    except Exception:
        setsid = None

    return Popen(
        args,
        stdout=stdout,
        stderr=stderr,
        stdin=PIPE,
        startupinfo=STARTUP_INFO,
        shell=shell,
        env=ev,
        cwd=cwd,
        preexec_fn=setsid,
        bufsize=bufsize,
    )


def is_a(v: Any, base: Any) -> bool:
    """Returns if v is an instance of type(base).
    """
    return isinstance(v, type(base))


def is_a_string(v: Any) -> bool:
    """Returns if v is an instance of str.
    """
    return isinstance(v, str)


# TODO: fixup and colocate settings funcs
def settings_obj() -> sublime.Settings:
    return sublime.load_settings("GoSublime.sublime-settings")


def aso() -> sublime.Settings:
    """
    GoSublime-aux.sublime-settings
    Location: "Packages/User"
    Contents (JSON):
        - 9o command history: "9o.hist./$PATH": [$COMMANDS]
        - ann:                "a14.02.25-1"
        - install_version:    "r14.12.06-1"
        - version:            "r14.12.06-1"
    """
    return sublime.load_settings("GoSublime-aux.sublime-settings")


def save_aso() -> None:
    return sublime.save_settings("GoSublime-aux.sublime-settings")


def setting(k: str, d: Optional[T] = None) -> Optional[T]:
    return _settings.get(k, d)


def println(*a: Any) -> str:
    args = []
    args.append("\n** %s **:" % datetime.datetime.now())
    for s in a:
        if isinstance(s, str):
            args.append(s.strip())
        elif isinstance(s, bytes) or isinstance(s, bytearray):
            args.append(ustr(s).strip())
        else:
            args.append(str(s))
    args.append("--------------------------------")

    msg = "%s\n" % "\n".join(args)
    print(msg)
    return msg


def debug(domain: str, *a: Any) -> None:
    # TODO (CEV): is this actually used ???
    if setting("_debug") is True:
        print("\n** DEBUG ** %s ** %s **:" % (domain, datetime.datetime.now()))
        for s in a:
            if isinstance(s, str):
                print(s.strip())
            elif isinstance(s, bytes) or isinstance(s, bytearray):
                print(ustr(s).strip())
            else:
                print(str(s))
        print("--------------------------------")


def log(*a: Any) -> None:
    try:
        LOGFILE.write(println(*a))
        LOGFILE.flush()
    except Exception:
        pass


def notify(domain: str, txt: str) -> None:
    txt = "%s: %s" % (domain, txt)
    status_message(txt)


def notice(domain: str, txt: str) -> None:
    error(domain, txt)


def error(domain: str, txt: str) -> None:
    txt = "%s: %s" % (domain, txt)
    log(txt)
    status_message(txt)


def error_traceback(domain: str, status_txt: str = "") -> None:
    tb = traceback().strip()
    if status_txt:
        prefix = "%s\n" % status_txt
    else:
        prefix = ""
        i = tb.rfind("\n")
        if i > 0:
            status_txt = tb[i:].strip()
        else:
            status_txt = tb

    log("%s: %s%s" % (domain, prefix, tb))
    status_message("%s: %s" % (domain, status_txt))


def notice_undo(
    domain: str,
    txt: str,
    view: sublime.View,
    should_undo: bool,
) -> None:
    def cb() -> None:
        if should_undo:
            view.run_command("undo")
        notice(domain, txt)

    sublime.set_timeout(cb, 0)


# WARN (CEV): can this be cleaned up ???
def show_output(
    domain: str,
    s: str,
    print_output: bool = True,
    syntax_file: str = "",
    replace: bool = True,
    merge_domain: bool = False,
    scroll_end: bool = False,
) -> None:
    def cb(domain: str, s: str, print_output: bool, syntax_file: str) -> None:
        panel_name = "%s-output" % domain
        if merge_domain:
            s = "%s: %s" % (domain, s)
            if print_output:
                println(s)
        elif print_output:
            println("%s: %s" % (domain, s))

        win = sublime.active_window()
        if win:
            panel = win.get_output_panel(panel_name)
            if panel:
                panel.run_command(
                    "gs_set_output_panel_content",
                    {
                        "content": s,
                        "syntax_file": syntax_file,
                        "scroll_end": scroll_end,
                        "replace": replace,
                    },
                )
            win.run_command("show_panel", {"panel": "output.%s" % panel_name})

    sublime.set_timeout(lambda: cb(domain, s, print_output, syntax_file), 0)


def is_pkg_view(view: Optional[sublime.View] = None) -> bool:
    # todo implement this fully
    return is_go_source_view(view, False)


def is_go_source_view(
    view: Optional[sublime.View] = None,
    strict: bool = True,
) -> bool:
    if view is not None:
        sel = view.sel()
        if sel is not None and len(sel) > 0:
            if view.score_selector(sel[0].begin(), "source.go") > 0:
                return True
            elif strict:
                return False
            else:
                fn = view.file_name() or ""
                return fn.endswith(".go")
    return False


def active_valid_go_view(
    win: Optional[sublime.Window] = None,
    strict: bool = True,
) -> Optional[sublime.View]:
    if not win:
        win = sublime.active_window()
    if win:
        view = win.active_view()
        if view and is_go_source_view(view, strict):
            return view
    return None


def rowcol(view: sublime.View) -> Tuple[int, int]:
    return view.rowcol(sel(view).begin())


_IS_WINDOWS: bool = os.name == "nt"


def os_is_windows() -> bool:
    return _IS_WINDOWS


def getenv(name: str, default: str = "", m: Dict[str, str] = {}) -> Any:
    return env(m).get(name, default)


def env(m: Dict[str, str] = {}) -> Dict[str, str]:
    """
    Assemble environment information needed for correct operation. In particular,
    ensure that directories containing binaries are included in PATH.
    """
    e = os.environ.copy()
    e.update(environ9)
    e.update(m)

    roots = lst(e.get("GOPATH", "").split(os.pathsep), e.get("GOROOT", ""))
    lfn = attr("last_active_go_fn", "")
    comps = lfn.split(os.sep)
    gs_gopath = []
    for i, s in enumerate(comps):
        if s.lower() == "src":
            p = os.sep.join(comps[:i])
            if p not in roots:
                gs_gopath.append(p)
    gs_gopath.reverse()
    e["GS_GOPATH"] = os.pathsep.join(gs_gopath)

    uenv = _settings.get("env", {})
    for k in uenv:
        try:
            uenv[k] = string.Template(uenv[k]).safe_substitute(e)
        except Exception as ex:
            println("%s: Cannot expand env var `%s`: %s" % (NAME, k, ex))

    e.update(uenv)
    e.update(m)

    # For custom values of GOPATH, installed binaries via go install
    # will go into the "bin" dir of the corresponding GOPATH path.
    # Therefore, make sure these paths are included in PATH.

    add_path = [home_dir_path("bin")]

    for s in lst(e.get("GOROOT", ""), e.get("GOPATH", "").split(os.pathsep)):
        if s:
            s = os.path.join(s, "bin")
            if s not in add_path:
                add_path.append(s)

    gobin = e.get("GOBIN", "")
    if gobin and gobin not in add_path:
        add_path.append(gobin)

    if os_is_windows():
        l = ["~\\bin", "~\\go\\bin", "C:\\Go\\bin"]
    else:
        l = [
            "~/bin",
            "~/go/bin",
            "/usr/local/go/bin",
            "/usr/local/opt/go/bin",
            "/usr/local/bin",
            "/usr/bin",
        ]

    for s in l:
        s = os.path.expanduser(s)
        if s not in add_path:
            add_path.append(s)

    for s in e.get("PATH", "").split(os.pathsep):
        if s and s not in add_path:
            add_path.append(s)

    e["PATH"] = os.pathsep.join(add_path)

    # Ensure no unicode objects leak through. The reason is twofold:
    #   * On Windows, Python 2.6 (used by Sublime Text) subprocess.Popen
    #     can only take bytestrings as environment variables in the
    #     "env" parameter. Reference:
    #     https://github.com/DisposaBoy/GoSublime/issues/112
    #     http://stackoverflow.com/q/12253014/1670
    #   * Avoids issues with networking too.
    clean_env = {}
    for k, v in e.items():
        try:
            clean_env[astr(k)] = astr(v)
        except Exception as ex:
            println("%s: Bad env: %s" % (NAME, ex))

    return clean_env


def mirror_settings(so: Union[Dict[str, Any], sublime.Settings]) -> Dict[str, Any]:
    # TODO (CEV): this looks brittle - remove or fix
    m = {}
    for key, default in _default_settings.items():
        val = so.get(key, default)
        if val is not None:
            m[key] = copy.copy(val)
    return m


def sync_settings() -> None:
    _settings.update(mirror_settings(settings_obj()))


def view_fn(view: sublime.View) -> str:
    """Returns the file name of the view, or the view id.  The view id is
    formatted as: 'gs.view://$ID'.
    """
    if view is not None:
        name = view.file_name()
        if name:
            return name
        else:
            return "gs.view://%s" % view.id()
    else:
        return ""


def view_src(view: sublime.View) -> str:
    """Returns the string source of the Sublime view.
    """
    if view:
        return view.substr(sublime.Region(0, view.size()))
    return ""


def win_view(
    vfn: Optional[str] = None,
    win: Optional[sublime.Window] = None,
) -> Tuple[sublime.Window, Optional[sublime.View]]:
    """Returns the window and view for view name or id vfn and window win.

    VFN:
      - File name: opens the named file in the window and returns the
        corresponding view, if the file is already open it is brought
        to the front.
      - View ID: must be formatted as 'gs.view://$ID'
      - "<stdin>": returns the active view in the window
      - None: returns the active view in the window

    Win:
      - None: win is set to the active window, if any.
    """
    if not win:
        win = sublime.active_window()

    view = None
    if win:
        m = VFN_ID_PAT.match(vfn or "")
        if m:
            try:
                vid = int(m.group(1))
                for v in win.views():
                    if v.id() == vid:
                        view = v
                        break
            except Exception:
                error_traceback(NAME)
        elif not vfn or vfn == "<stdin>":
            view = win.active_view()
        else:
            view = win.open_file(vfn)
    return (win, view)


# TODO (CEV): use the logic from GsDocCommand.jump()
def do_focus(
    fn: str,
    row: int,
    col: int,
    win: Optional[sublime.Window],
    focus_pat: str,
    cb: Optional[Callable[[bool], None]],
) -> None:
    win, view = win_view(fn, win)
    if win is None or view is None:
        notify(NAME, "Cannot find file position %s:%s:%s" % (fn, row, col))
        if cb:
            cb(False)
    elif view.is_loading():
        focus(fn, row=row, col=col, win=win, focus_pat=focus_pat, cb=cb)
    else:
        win.focus_view(view)
        if row <= 0 and col <= 0 and focus_pat:
            r = view.find(focus_pat, 0)
            if r:
                row, col = view.rowcol(r.begin())
        view.run_command("gs_goto_row_col", {"row": row, "col": col})
        if cb:
            cb(True)


# TODO (CEV): use the logic from GsDocCommand.jump()
def focus(
    fn: str,
    row: int = 0,
    col: int = 0,
    win: Optional[sublime.Window] = None,
    timeout: int = 100,
    focus_pat: str = "^package ",
    cb: Optional[Callable[[bool], None]] = None,
) -> None:
    sublime.set_timeout(lambda: do_focus(fn, row, col, win, focus_pat, cb), timeout)


def sm_cb() -> None:
    global sm_text
    global sm_set_text
    global sm_frame

    with sm_lck:
        ntasks = len(sm_tasks)
        tm = sm_tm
        s = sm_text
        if s:
            delta = datetime.datetime.now() - tm
            if delta.seconds >= 10:
                sm_text = ""

    if ntasks > 0:
        if s:
            s = u"%s, %s" % (sm_frames[sm_frame], s)
        else:
            s = u"%s" % sm_frames[sm_frame]

        if ntasks > 1:
            s = "%d %s" % (ntasks, s)

        sm_frame = (sm_frame + 1) % len(sm_frames)

    if s != sm_set_text:
        sm_set_text = s
        st2_status_message(s)

    sched_sm_cb()


def sched_sm_cb() -> None:
    sublime.set_timeout(sm_cb, 250)


def status_message(s: str) -> None:
    # WARN (CEV): WTF is going on here ???
    global sm_text
    global sm_tm
    with sm_lck:
        sm_text = s
        sm_tm = datetime.datetime.now()


# WARN WARN WARN
def begin_x(
    domain: str,
    message: str,
    set_status: bool = True,
    cancel: Callable[[], None] = None,
) -> str:
    global sm_task_counter

    if message and set_status:
        status_message("%s: %s" % (domain, message))

    with sm_lck:
        sm_task_counter += 1
        tid = "t%d" % sm_task_counter
        sm_tasks[tid] = {
            "start": datetime.datetime.now(),
            "domain": domain,
            "message": message,
            "cancel": cancel,
        }

    return tid


def begin(
    domain: str,
    message: str,
    set_status: bool = True,
    cancel: Callable[[], None] = None,
) -> str:
    global sm_task_counter

    if message and set_status:
        status_message("%s: %s" % (domain, message))

    with sm_lck:
        sm_task_counter += 1
        tid = "t%d" % sm_task_counter
        sm_tasks[tid] = {
            "start": datetime.datetime.now(),
            "domain": domain,
            "message": message,
            "cancel": cancel,
        }

    return tid


def end(task_id: str) -> None:
    with sm_lck:
        if task_id in sm_tasks:
            del sm_tasks[task_id]


def task(task_id: str) -> Optional[TaskT]:
    with sm_lck:
        return sm_tasks.get(task_id, None)


def task_list() -> List[Tuple[str, TaskT]]:
    with sm_lck:
        return sorted(sm_tasks.items())


def cancel_task(tid: str) -> bool:
    t = task(tid)
    if t and t["cancel"]:
        s = "are you sure you want to end task: #%s %s: %s" % (
            tid,
            t["domain"],
            t["message"],
        )
        if sublime.ok_cancel_dialog(s):
            t["cancel"]()  # noqa
        return True
    return False


def show_quick_panel(items, cb=None):
    def f():
        win = sublime.active_window()
        if win is not None:
            if callable(cb):
                f = lambda i: cb(i, win)
            else:
                f = lambda i: None

            win.show_quick_panel(items, f, sublime.MONOSPACE_FONT)

    sublime.set_timeout(f, 0)


def go_env_goroot():
    # WARN: Appears broken and not used - REMOVE.
    out, _, _ = runcmd(["go env GOROOT"], shell=True)
    return out.strip().encode("utf-8")


def list_dir_tree(dirname, filter, exclude_prefix=(".", "_")):
    lst = []

    try:
        for fn in os.listdir(dirname):
            if fn[0] in exclude_prefix:
                continue

            basename = fn.lower()
            fn = os.path.join(dirname, fn)

            if os.path.isdir(fn):
                lst.extend(list_dir_tree(fn, filter, exclude_prefix))
            else:
                if filter:
                    pathname = fn.lower()
                    _, ext = os.path.splitext(basename)
                    ext = ext.lstrip(".")
                    if filter(pathname, basename, ext):
                        lst.append(fn)
                else:
                    lst.append(fn)
    except Exception:
        pass

    return lst


def traceback(domain: str = "GoSublime") -> str:
    """Returns a traceback formatted as 'domain: traceback'.
    """
    return "%s: %s" % (domain, tbck.format_exc())


def ustr(s: Union[bytes, bytearray, str]) -> str:
    """Returns a decoded version of the string s.  If s is a unicode string it
    is not decoded.

    The codecs used are 'utf-8' and locale.getpreferredencoding(), if not
    'utf-8'.  The codecs are stored in in TRY_ENCODINGS.
    """
    if isinstance(s, str):
        return s
    elif isinstance(s, bytes) or isinstance(s, bytearray):
        for enc in TRY_ENCODINGS:
            try:
                return s.decode(enc, "strict")
            except UnicodeDecodeError:
                continue
        return s.decode("utf-8", "replace")
    else:
        raise TypeError("invalid type: {}".format(type(s)))


def astr(s: Any) -> str:
    """Returns an encoded version of the string s as a bytes (str) object.
    """
    if isinstance(s, str):
        return s
    else:
        return str(s)


def lst(*a: Any) -> List[Any]:
    """Returns arguments *a as a flat list, any list arguments are flattened.
    Example: lst(1, [2, 3]) returns [1, 2, 3].
    """
    flat = []
    for v in a:
        if isinstance(v, list):
            flat.extend(v)
        else:
            flat.append(v)
    return flat


def dval(val: Optional[T], default: T) -> T:
    """Default Value: returns v if v is not None and of type d,
    otherwise d is returned.
    """
    if val is not None and isinstance(val, type(default)):
        return val
    else:
        return default


def tm_path(name: str) -> str:
    """Returns the path of the settings file for name ('9o', 'doc', 'go',
    'gohtml')

    Note: This is used to locate syntax files, and appears to break when
    GoSublime is not located in the ST Package directory.
    """
    pkg = "Packages/GoSubl/"
    if name == "9o":
        return pkg + "syntax/GoSublime-9o.tmLanguage"
    elif name == "doc":
        return pkg + "GsDoc.hidden-tmLanguage"
    elif name == "go":
        return pkg + "syntax/GoSublime-Go.tmLanguage"
    elif name == "gohtml":
        return pkg + "syntax/GoSublime-HTML.tmLanguage"
    elif name == "go":
        so = sublime.load_settings("GoSublime-next.sublime-settings")
        if so:
            exts: Optional[List[str]] = so.get("extensions")
            if exts and "go" in exts:
                return pkg + "GoSublime-next.tmLanguage"

    # WARN: should we raise an execption here ???
    notice(NAME, "invalid settings file name: %s" % name)
    return ""


def packages_dir() -> str:
    """Returns the path of the Sublime Text User Package ("Packages/User").
    """
    # TODO (CEV): replacde this with a variable
    fn = attr("gs.packages_dir")
    if not fn:
        fn = sublime.packages_path()
        set_attr("gs.packages_dir", fn)
    return fn


def dist_path(*a: str) -> str:
    """Returns the path of the GoSubl package.
    """
    return os.path.join(packages_dir(), "GoSubl", *a)


def mkdirp(fn: str) -> None:
    """Recursively creates a directory rooted at fn, if directory fn does not
    exist.
    """
    try:
        os.makedirs(fn)
    except:
        pass


def _home_path(*paths: str) -> str:
    """Returns the path of the platform specific (OS and ARCH) GoSublime home
    directory in the Sublime Text User Package expanded to path *paths.

    For example if *paths equals 'bin' and the platform is 'osx-x64', _home_path
    returns 'Sublime Text 3/Packages/User/GoSublime/osx-x64/bin'.
    """
    return os.path.join(
        packages_dir(),
        "User",
        "GoSublime",
        about.PLATFORM,
        *paths,
    )


def home_dir_path(*path: str) -> str:
    """Recursively creates a directory rooted at the GoSublime platform specific
    home directory joined with *path.

    For example if *path equals 'bin' and the platform is 'osx-x64', home_path
    creates a directory at 'Sublime Text 3/Packages/User/GoSublime/osx-x64/bin'.
    """
    fn = _home_path(*path)
    mkdirp(fn)
    return fn


def home_path(*path: str) -> str:
    """Recursively creates the directory rooted at the directory of *path,
    within the GoSublime platform specific home directory.

    For example if *path is ('bin', 'margo.exe') and the platform is 'osx-x64',
    home_path creates a directory at: 'Packages/User/GoSublime/osx-x64/bin'.
    """
    fn = _home_path(*path)
    mkdirp(os.path.dirname(fn))
    return fn


def init_log_file(
    max_bytes: int = MAX_LOG_BYTES,
    backup_count: int = 5,
) -> str:
    if system() == "Darwin":
        fp = Path("~/Library/Logs/GoSublime/gosubl.log").expanduser()
    else:
        fp = Path(home_path()) / "gosubl.log"

    if not fp.exists():
        fp.parent.mkdir(parents=True, exist_ok=True)
        fp.touch()

    elif max_bytes > 0 and fp.stat().st_size >= max_bytes:
        ts = int(datetime.datetime.utcnow().timestamp())
        new = fp.parent / f"{ts}.{fp.name}.gz"
        if not new.exists():
            with open(fp, 'rb') as f_in:
                with gzip.open(new, 'xb') as f_out:
                    copyfileobj(f_in, f_out)
            fp.unlink()  # delete
            fp.touch()   # recreate

    if backup_count > 0:
        backups = sorted(fp.parent.glob("*.gz"))
        while backups and len(backups) > backup_count:
            backups.pop(0).unlink(missing_ok=True)

    return str(fp)


# TODO: these are only used in sed/recv and should be removed
def json_decode(s, default):
    """Decodes JSON s and checks if it is an instance of default.
    Returning the decoded object and an error message, if any.
    """
    try:
        res = json.loads(s)
        if is_a(res, default):
            return (res, "")
        return (res, "Unexpected value type")
    except Exception as ex:
        return (default, "Decode Error: %s" % ex)


# TODO: these are only used in sed/recv and should be removed
def json_encode(a: Any) -> Tuple[str, str]:
    """Returns a encoded into JSON and an error message, if any.
    """
    try:
        return (json.dumps(a), "")
    except Exception as ex:
        return ("", "Encode Error: %s" % ex)


def attr(k: str, d: Optional[Any] = None) -> Optional[Any]:
    """Returns the _attr with key k, or d if not found.
    """
    with _attr_lck:
        v = _attr.get(k, None)
        return d if v is None else copy.copy(v)


def set_attr(k: str, v: Any) -> None:
    """Sets the _attr key k to value v.
    """
    with _attr_lck:
        _attr[k] = v


def del_attr(k: str) -> Optional[Any]:
    """Deletes the _attr with key k.
    """
    v = None
    with _attr_lck:
        if k in _attr:
            v = _attr[k]
            del _attr[k]
        return v


# note: this functionality should not be used inside this module
# continue to use the try: X except: X=Y hack
def checked(domain: str, k: str) -> bool:
    with _checked_lck:
        k = "common.checked.%s.%s" % (domain, k)
        v = _checked.get(k, False)
        _checked[k] = True
    return v


def sel(view: sublime.View, i: int = 0) -> sublime.Region:
    try:
        s = view.sel()
        if s is not None and i < len(s):
            return s[i]
    except Exception:
        pass
    return sublime.Region(0, 0)


def which_ok(fn: str) -> bool:
    try:
        return os.path.isfile(fn) and os.access(fn, os.X_OK)
    except Exception:
        return False


# WARN (CEV): Module initialization?
# WTF is going on here?

sm_lck = threading.Lock()
# TODO (CEV): replace this with a dedicated counter
sm_task_counter = 0
# WARN (CEV): make sure this is correct or use a typed dict
sm_tasks: Dict[str, TaskT] = {}
sm_frame = 0
sm_frames = (u"\u25D2", u"\u25D1", u"\u25D3", u"\u25D0")
sm_tm = datetime.datetime.now()
sm_text = ""
sm_set_text = ""

st2_status_message = sublime.status_message
sublime.status_message = status_message


# WARN (CEV): WTF is this ???
try:
    gs9o
except Exception:
    gs9o = {}


def gs_init(m={}):
    """init gs module.
    """

    global LOGFILE
    try:
        LOGFILE = open(init_log_file(), "a+")
    except Exception as ex:
        LOGFILE = open(os.devnull, "w")
        notice(
            NAME,
            "Cannot create log file. Remote(margo) and persistent logging will be disabled. Error: %s"
            % ex,
        )
        raise ex

    sched_sm_cb()

    settings_obj().clear_on_change("GoSublime.settings")
    settings_obj().add_on_change("GoSublime.settings", sync_settings)
    sync_settings()
