import atexit
import base64
import json
import os
import threading
import time

from collections import OrderedDict
from secrets import token_hex
from typing import Any
from typing import Callable
from typing import Dict
from typing import List
from typing import Optional
from typing import Tuple
from typing import TypedDict
from typing import cast

import sublime

from gosubl import about
from gosubl import ev
from gosubl import gs
from gosubl import gsq
from gosubl import sh
from gosubl.utils import Counter

DOMAIN = "MarGo"
REQUEST_PREFIX = "%s.rqst." % DOMAIN
PROC_ATTR_NAME = "mg9.proc"
TAG = about.VERSION
INSTALL_VERSION = about.VERSION
INSTALL_EXE = about.MARGO_EXE

REQUEST_RAND_ID = token_hex(3)
REQUEST_COUNTER = Counter()


def gs_init(m={}) -> None:
    """Called by GoSublime.py::plugin_loaded
        m = {
                'version': VERSION,
                'ann': ANN,
                'margo_exe': MARGO_EXE,
            }
    """

    global INSTALL_VERSION
    global INSTALL_EXE

    # Kill server on process exit.
    atexit.register(killSrv)

    version = m.get("version")
    if version:
        INSTALL_VERSION = version

    margo_exe = m.get("margo_exe")
    if margo_exe:
        INSTALL_EXE = margo_exe

    force = about.FORCE_INSTALL is True
    # install_version recorded in 'GoSublime-aux.sublime-settings'
    aso_install_vesion = gs.aso().get("install_version", "")

    def install_fn() -> None:
        install(aso_install_vesion, force)

    # GsQ handles threaded processes.
    # Install latest version.
    gsq.do("GoSublime", install_fn, msg="Installing MarGo", set_status=False)


class Request:
    __slots__ = "callback", "method", "token", "_start"

    def __init__(
        self,
        callback: Callable,
        method: str = "",
        token: str = "",
    ) -> None:
        self.callback = callback
        self.method = method
        self.token = token or "mg9.autoken.{}.{}".format(
            REQUEST_RAND_ID, REQUEST_COUNTER.next(),
        )
        self._start = time.time()

    def header(self) -> Dict[str, str]:
        return {"method": self.method, "token": self.token}

    def reset_start_time(self) -> None:
        self._start = time.time()

    def duration(self) -> float:
        return time.time() - self._start


def _inst_state():
    """Returns install state from gs.attr.
    """
    # TODO: Improve the handling of install state.
    return gs.attr(_inst_name(), "")


def _inst_name():
    """Returns the install name for the install version.
    """
    return "mg9.install.%s" % INSTALL_VERSION


def _margo_src():
    return gs.dist_path("margo9")


def _margo_bin(exe=""):
    """Returns the path of the margo executable.
    """
    return gs.home_path("bin", exe or INSTALL_EXE)


def sanity_check_sl(sl):
    n = 0
    for p in sl:
        n = max(n, len(p[0]))

    t = "%d" % n
    t = "| %" + t + "s: %s"
    indent = "| %s> " % (" " * n)

    a = "~%s" % os.sep
    b = os.path.expanduser(a)

    return [
        t % (k, gs.ustr(v).replace(b, a).replace("\n", "\n%s" % indent)) for k, v in sl
    ]


def sanity_check(env={}, error_log=False):
    if not env:
        env = sh.env()

    ns = "(not set)"

    sl = [
        ("install state", _inst_state()),
        ("sublime.version", sublime.version()),
        ("sublime.channel", sublime.channel()),
        ("about.ann", gs.attr("about.ann", "")),
        ("about.version", gs.attr("about.version", "")),
        ("version", about.VERSION),
        ("platform", about.PLATFORM),
        ("~bin", "%s" % gs.home_dir_path("bin")),
        ("margo.exe", "%s (%s)" % _tp(_margo_bin())),
        ("go.exe", "%s (%s)" % _tp(sh.which("go") or "go")),
        ("go.version", sh.GO_VERSION),
        ("GOROOT", "%s" % env.get("GOROOT", ns)),
        ("GOPATH", "%s" % env.get("GOPATH", ns)),
        ("GOBIN", "%s (should usually be `%s`)" % (env.get("GOBIN", ns), ns)),
        ("set.shell", str(gs.lst(gs.setting("shell")))),
        ("env.shell", env.get("SHELL", "")),
        ("shell.cmd", str(sh.cmd("${CMD}"))),
    ]

    if error_log:
        try:
            with open(gs.home_path("log.txt"), "r") as f:
                s = f.read().strip()
                sl.append(("error log", s))
        except Exception:
            pass

    return sl


def _sb(s):
    bdir = gs.home_dir_path("bin")
    if s.startswith(bdir):
        s = "~bin%s" % (s[len(bdir) :])
    return s


def _tp(s):
    return (_sb(s), ("ok" if os.path.exists(s) else "missing"))


def _bins_exist():
    return os.path.exists(_margo_bin())


def maybe_install():
    """Installs margo if installer is not busy or the binary does not exist.
    """
    if _inst_state() == "" and not _bins_exist():
        install("", True)


def install(aso_install_vesion, force_install):
    """Install GoSublime margo.
    """

    global INSTALL_EXE

    if _inst_state() != "":
        gs.notify(
            DOMAIN,
            "Installation aborted. Install command already called for GoSublime %s."
            % INSTALL_VERSION,
        )
        return

    INSTALL_EXE = INSTALL_EXE.replace(
        "_%s.exe" % about.DEFAULT_GO_VERSION, "_%s.exe" % sh.GO_VERSION
    )
    about.MARGO_EXE = INSTALL_EXE

    is_update = about.VERSION != INSTALL_VERSION

    gs.set_attr(_inst_name(), "busy")

    init_start = time.time()

    if (
        not is_update
        and not force_install
        and _bins_exist()
        and aso_install_vesion == INSTALL_VERSION
    ):
        m_out = "no"
    else:
        gs.notify("GoSublime", "Installing MarGo")
        start = time.time()

        gopath = gs.dist_path() + os.pathsep + sh.getenv("GOPATH")
        margo_binary = os.path.join(gs.home_dir_path("bin"), INSTALL_EXE)

        if os.path.exists(os.path.join(
            gs.dist_path(), 'src', 'gosubli.me', 'margo', 'vendor',
        )):
            cmd = sh.Command(["go", "mod", "vendor"])
            cmd.wd = os.path.join(gs.dist_path(), 'src', 'gosubli.me', 'margo')
            cmd.run()

        cmd = sh.Command(
            ["go", "build", "-v", "-x", "-o", margo_binary, "gosubli.me/margo"],
        )

        cmd.wd = os.path.join(gs.dist_path(), 'src', 'gosubli.me', 'margo')

        cmd.env = {
            "CGO_ENABLED": "0",
            "GOBIN": "",
            "GOPATH": gopath,
            "GO111MODULE": "on",
        }

        # ev.debug("%s.build" % DOMAIN, {"cmd": cmd.cmd_lst, "cwd": cmd.wd})

        cr = cmd.run()
        m_out = "cmd: `%s`\nstdout: `%s`\nstderr: `%s`\nexception: `%s`" % (
            cr.cmd_lst,
            cr.out.strip(),
            cr.err.strip(),
            cr.exc,
        )

        if cr.ok and _bins_exist():

            def f():
                gs.aso().set("install_version", INSTALL_VERSION)
                gs.save_aso()

            sublime.set_timeout(f, 0)
        else:
            err_prefix = "MarGo build failed"
            gs.error(DOMAIN, "%s\n%s" % (err_prefix, m_out))

            sl = [
                (
                    "GoSublime error",
                    "\n".join(
                        (
                            err_prefix,
                            "This is possibly a bug or miss-configuration of your environment.",
                            "For more help, please file an issue with the following build output",
                            "at: https://github.com/DisposaBoy/GoSublime/issues/new",
                            "or alternatively, you may send an email to: gosublime@dby.me",
                            "\n",
                            m_out,
                        )
                    ),
                )
            ]
            sl.extend(sanity_check({}, False))
            gs.show_output("GoSublime", "\n".join(sanity_check_sl(sl)))

    gs.set_attr(_inst_name(), "done")

    if is_update:
        gs.show_output(
            "GoSublime-source",
            "\n".join(
                [
                    "GoSublime source has been updated.",
                    "New version: `%s`, current version: `%s`"
                    % (INSTALL_VERSION, about.VERSION),
                    "Please restart Sublime Text to complete the update.",
                ]
            ),
        )
    else:
        e = sh.env()
        a = ["GoSublime init %s (%0.3fs)" % (INSTALL_VERSION, time.time() - init_start)]

        sl = [("install margo", m_out)]
        sl.extend(sanity_check(e))
        a.extend(sanity_check_sl(sl))
        gs.println(*a)

        missing = [k for k in ("GOROOT", "GOPATH") if not e.get(k)]
        if missing:
            missing_message = "\n".join(
                [
                    "Missing required environment variables: %s" % " ".join(missing),
                    "See the `Quirks` section of USAGE.md for info",
                ]
            )

            cb = lambda ok: gs.show_output(
                DOMAIN, missing_message, merge_domain=True, print_output=False
            )
            gs.error(DOMAIN, missing_message)
            gs.focus(gs.dist_path("USAGE.md"), focus_pat="^Quirks", cb=cb)

        killSrv()

        start = time.time()
        # acall('ping', {}, lambda res, err: gs.println('MarGo Ready %0.3fs' % (time.time() - start)))

        report_x = lambda: gs.println(
            "GoSublime: Exception while cleaning up old binaries", gs.traceback()
        )
        try:
            bin_dirs = [gs.home_path("bin")]

            l = []
            for d in bin_dirs:
                try:
                    for fn in os.listdir(d):
                        if fn != INSTALL_EXE and about.MARGO_EXE_PAT.match(fn):
                            l.append(os.path.join(d, fn))
                except Exception:
                    pass

            for fn in l:
                try:
                    gs.println("GoSublime: removing old binary: `%s'" % fn)
                    os.remove(fn)
                except Exception:
                    report_x()

        except Exception:
            report_x()


CompleteCandidate = TypedDict('CompleteCandidate', {
    'class': str,
    'package': Optional[str],  # Empty for packages
    'name': str,
    'type': Optional[str],  # Empty for packages
    'receiver': Optional[str],
})


# TODO: make the Candidate optional as well
CompleteResponse = Tuple[List[CompleteCandidate], Optional[str]]


# TODO: use view.id() and view.change_id()[0] as the cache key
def calltip(fn: str, src: str, pos: int, quiet: bool, f: Callable) -> None:
    tid = ""
    if not quiet:
        tid = gs.begin(DOMAIN, "Fetching calltips")

    # # Move pos to the end of the symbol to improve cache performance
    # for i in range(pos, len(src)):
    #     c = src[i]
    #     if not c.isalnum() and c != '_':
    #         if i > pos:
    #             pos = i - 1
    #         break

    def cb(res, err):
        if tid:
            gs.end(tid)

        res = gs.dval(res.get("Candidates"), [])
        f(res, err)

    acall("gocode_calltip", _complete_opts(fn, src, pos, True), cb)


def _complete_opts(
    filename: str,
    source: str,
    cursor: int,
    builtins: bool,
) -> Dict[str, Any]:
    return {
        "Dir": gs.basedir_or_cwd(filename),
        "Builtins": builtins,
        "Fn": filename or "",
        "Src": source or "",
        "Pos": cursor or 0,
        # WARN: removing "Home" since we don't use it
        # "Home": sh.vdir(),
        "Autoinst": gs.setting("autoinst"),
        "InstallSuffix": gs.setting("installsuffix", ""),
        # TODO: can we just omit this if there are no GOPATH overrides
        # and what are we missing by not using the full sh.env() func?
        "Env": sh.complete_environ(),
    }


def complete(
    filename: str,
    souce: str,
    cursor: int,
) -> Tuple[List[CompleteCandidate], Optional[str]]:
    builtins = bool(
        gs.setting("autocomplete_builtins") is True or
        gs.setting("complete_builtins") is True
    )
    res, err = bcall(
        "gocode_complete",
        _complete_opts(filename, souce, cursor, builtins),
    )
    return gs.dval(res.get("Candidates"), []), err


FormatResponse = TypedDict('FormatResponse', {
    'src': str,
    'no_change': bool,
})


def fmt(fn: str, src: str) -> Tuple[FormatResponse, Optional[str]]:
    x = gs.setting("fmt_cmd")
    if x:
        res, err = bcall(
            "sh",
            {"Env": sh.env(), "Cmd": {"Name": x[0], "Args": x[1:], "Input": src or ""}},
        )
        return res.get("out", ""), (err or res.get("err", ""))

    timeout = 0.8
    res, err = bcall(
        "fmt",
        {
            "filename": fn or "",
            "source": src or "",
            "tab_indent": gs.setting("fmt_tab_indent"),
            "tab_width": gs.setting("fmt_tab_width"),
            "timeout": timeout,
        },
        timeout=timeout,
    )
    return cast(FormatResponse, res), err


def import_paths(fn, src, f):
    tid = gs.begin(DOMAIN, "Fetching import paths")

    def cb(res, err):
        gs.end(tid)
        f(res, err)

    acall(
        "import_paths",
        {
            "fn": fn or "",
            "src": src or "",
            "env": sh.env(),
            "InstallSuffix": gs.setting("installsuffix", ""),
        },
        cb,
    )


def pkg_name(fn, src):
    res, err = bcall("pkg", {"fn": fn or "", "src": src or ""})
    return res.get("name"), err


def pkg_dirs(f):
    tid = gs.begin(DOMAIN, "Fetching pkg dirs")

    def cb(res, err):
        gs.end(tid)
        f(res, err)

    acall("pkg_dirs", {"env": sh.env()}, cb)


# class TestFunc:
#     __slots__ = "name", "filename", "line"
#
#     def __init__(self, name: str, filename: str, line: int):
#         self.name = name
#         self.filename = filename
#         self.line = line
#
#
# class ListTestsResponse:
#     __slots__ = "tests", "benchmarks", "examples", "fuzztests"
#
#     def __init__(
#         self,
#         tests: Optional[List[TestFunc]] = None,
#         benchmarks: Optional[List[TestFunc]] = None,
#         examples: Optional[List[TestFunc]] = None,
#         fuzztests: Optional[List[TestFunc]] = None,
#     ):
#         self.tests = tests
#         self.benchmarks = benchmarks
#         self.examples = examples
#         self.fuzztests = fuzztests
#
#     @classmethod
#     def from_json(cls, data: Dict[str, Any]) -> "ListTestsResponse":
#         return None
#
#     def is_empty(self) -> bool:
#         return (
#             not self.tests and not self.benchmarks and
#             not self.examples and not self.fuzztests
#         )


# type TestFunc struct {
#     Name     string `json:"name"`
#     Filename string `json:"filename"`
#     Line     int    `json:"line"`
# }
#
# type TestFunctions struct {
#     Tests      []TestFunc `json:"tests,omitempty"`
#     Benchmarks []TestFunc `json:"benchmarks,omitempty"`
#     Examples   []TestFunc `json:"examples,omitempty"`
#     FuzzTests  []TestFunc `json:"fuzz_tests,omitempty"`
# }


class TestFunc(TypedDict):
    name: str
    filename: str
    line: int


class ListTestsResponse(TypedDict):
    tests: Optional[List[TestFunc]]
    benchmarks: Optional[List[TestFunc]]
    examples: Optional[List[TestFunc]]
    fuzz_tests: Optional[List[TestFunc]]


def list_go_tests(filename: str) -> Tuple[ListTestsResponse, Optional[str]]:
    raw, err = bcall("list_tests", {"filename": filename})
    return {
        "tests": raw.get("tests"),
        "benchmarks": raw.get("benchmarks"),
        "examples": raw.get("examples"),
        "fuzz_tests": raw.get("fuzz_tests"),
    }, err


def declarations(fn, src, pkg_dir, f):
    tid = gs.begin(DOMAIN, "Fetching declarations")

    def cb(res, err):
        gs.end(tid)
        f(res, err)

    return acall(
        "declarations",
        {"fn": fn or "", "src": src, "env": sh.env(), "pkgDir": pkg_dir},
        cb,
    )


def imports(fn, src, toggle):
    return bcall(
        "imports",
        {
            "autoinst": gs.setting("autoinst"),
            "env": sh.env(),
            "fn": fn or "",
            "src": src or "",
            "toggle": toggle or [],
            "tabIndent": gs.setting("fmt_tab_indent"),
            "tabWidth": gs.setting("fmt_tab_width"),
        },
    )


def rename(filename: str, rename_to: str, offset: int, callback: Any) -> Dict[str, str]:
    tid = gs.begin(DOMAIN, "Renaming symbol")

    def cb(res, err):
        gs.end(tid)
        callback(res, err)

    request = {
        "filename": filename,
        "to": rename_to,
        "offset": offset,
        "env": sh.env(),
    }
    acall("rename", request, cb)


# WARN (CEV): show preview of the returned references
#
# TODO: include src one we start talking to gopls directly
def references(filename, source, offset, callback):
    tid = gs.begin(DOMAIN, "Finding references")

    def cb(res, err):
        gs.end(tid)
        callback(res, err)

    request = {
        "filename": filename or "",
        "offset": offset or 0,
        "env": sh.env(),
    }
    acall("references", request, cb)
    pass


def doc(fn, src, offset, f):
    tid = gs.begin(DOMAIN, "Fetching doc info")

    def cb(res, err):
        gs.end(tid)
        f(res, err)

    acall(
        "doc",
        {
            "fn": fn or "",
            "src": src or "",
            "offset": offset or 0,
            "env": sh.env(),
            "tabIndent": gs.setting("fmt_tab_indent"),
            "tabWidth": gs.setting("fmt_tab_width"),
        },
        cb,
    )


def share(src, f):
    warning = (
        "Are you sure you want to share this file. It will be public on play.golang.org"
    )
    if sublime.ok_cancel_dialog(warning):
        acall("share", {"Src": src or ""}, f)
    else:
        f({}, "Share cancelled")


def acall(method: str, arg: Dict[str, Any], cb: Callable) -> None:
    """Asynchronous send to margo.
    """
    gs.mg9_send_q.put((method, arg, cb))


def bcall(
    method: str,
    arg: Dict[str, Any],
    timeout: Optional[float] = None,
) -> Tuple[Dict[str, Any], Optional[str]]:
    """Synchronous send to margo.
    """
    if _inst_state() != "done":
        return {}, "Blocking call(%s) aborted: Install is not done" % method

    q = gs.queue.Queue()

    acall(method, arg, lambda r, e: q.put((r, e)))
    if timeout is None:
        timeout = gs.setting("ipc_timeout", 1)
    try:
        res, err = q.get(True, timeout=timeout)
        return res, err
    except Exception as ex:
        return {}, "Blocking Call(%s): Timeout: %s" % (method, ex)


def expand_jdata(v):
    """Expands a byte or base64 encoded string.
    """
    if gs.is_a(v, {}):
        for k in v:
            v[k] = expand_jdata(v[k])
    elif gs.is_a(v, []):
        v = [expand_jdata(e) for e in v]
    else:
        if gs.PY3K and isinstance(v, bytes):
            v = gs.ustr(v)

        if gs.is_a_string(v) and v.startswith("base64:"):
            try:
                v = gs.ustr(base64.b64decode(v[7:]))
            except Exception:
                v = ""
                gs.error_traceback(DOMAIN)
    return v


def _recv():
    """Polls the mg9_recv_q queue parsing responses.
    """
    # TODO: REFACTOR.
    while True:
        try:
            ln = gs.mg9_recv_q.get()
            try:
                ln = ln.strip()
                if ln:
                    # WARN
                    # r, _ = gs.json_decode(ln, {})
                    r, err = gs.json_decode(ln, {})
                    if err:
                        print("### RECV (ERROR): {}\n{}\n###".format(err, ln))  # WARN

                    token = r.get("token", "")
                    tag = r.get("tag", "")
                    k = REQUEST_PREFIX + token

                    req: Request = gs.del_attr(k)
                    if req and req.callback:
                        if tag != TAG:
                            gs.notice(
                                DOMAIN,
                                "\n".join(
                                    [
                                        "GoSublime/MarGo appears to be out-of-sync.",
                                        "Maybe restart Sublime Text.",
                                        "Received tag `%s', expected tag `%s'. "
                                        % (tag, TAG),
                                    ]
                                ),
                            )

                        err = r.get("error", "")

                        # # TODO: Check if debug is enabled (len()).
                        # ev.debug(
                        #     DOMAIN,
                        #     "margo response: %s"
                        #     % {
                        #         "method": req.method,
                        #         "tag": tag,
                        #         "token": token,
                        #         "dur": "%0.3fs" % req.duration(),
                        #         "err": err,
                        #         "size": "%0.1fK" % (len(ln) / 1024.0),
                        #     },
                        # )

                        # CEV: req.f is the callback 'cb' set in _send().
                        #
                        dat = expand_jdata(r.get("data", {}))
                        # print("### RECV: DATA: {}".format(dat))  # WARN
                        try:
                            # Add request back to the attr dict.
                            #
                            # TODO: Document, which calls keep the request.
                            keep = req.callback(dat, err) is True
                            if keep:
                                req.reset_start_time()
                                gs.set_attr(k, req)
                        except Exception:
                            gs.error_traceback(DOMAIN)
                    else:
                        pass
                        # ev.debug(DOMAIN, "Ignoring margo: token: %s" % token)
            except Exception:
                gs.println(gs.traceback())
        except Exception:
            gs.println(gs.traceback())
            break


def _send():
    """Polls the mg9_send_q queue, sending requests to margo.  If the margo
    process is not running _send() starts it and sets the PROC_ATTR_NAME attr.
    """
    # TODO: REFACTOR.
    while True:
        try:
            try:
                method, arg, cb = gs.mg9_send_q.get()

                # CEV: proc should be the margo process.
                proc = gs.attr(PROC_ATTR_NAME)

                # CEV: Looks like this starts/restarts the margo process.
                if not proc or proc.poll() is not None:
                    # print("###: PROC DIED")  # WARN
                    killSrv()

                    if _inst_state() != "busy":
                        maybe_install()

                    # TODO: Improve the handling of install state.
                    while _inst_state() == "busy":
                        time.sleep(0.100)

                    # Margo path and command line options.
                    mg_bin = _margo_bin()
                    cmd = [
                        mg_bin,
                        "-oom",
                        gs.setting("margo_oom", 0),
                        "-poll",
                        30,
                        "-tag",
                        TAG,
                    ]
                    # WARN: enable debugging
                    if about.MARGO_PPROF_ADDR:
                        cmd += ["-pprof-addr", about.MARGO_PPROF_ADDR]

                    c = sh.Command(cmd)
                    c.env = {"GOGC", "200"}
                    c.stderr = gs.LOGFILE

                    # WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN
                    # WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN
                    # WARN (CEV): seeting GOGC
                    # c.env = {"GOGC": 10, "XDG_CONFIG_HOME": gs.home_path()}
                    c.env = {"XDG_CONFIG_HOME": gs.home_path()}

                    pr = c.proc()
                    if pr.ok:
                        proc = pr.p
                        err = ""
                    else:
                        proc = None
                        err = "Exception: %s" % pr.exc

                    if err or not proc or proc.poll() is not None:
                        killSrv()
                        _call(cb, {}, "Abort. Cannot start MarGo: %s" % err)

                        continue

                    # Set the process name
                    gs.set_attr(PROC_ATTR_NAME, proc)
                    # Launch stdout feed.
                    gsq.launch(DOMAIN, lambda: _read_stdout(proc))

                    # WARN WARN WARN WARN
                    # gsq.launch(DOMAIN, lambda: _read_stderr(proc))

                req = Request(callback=cb, method=method)
                gs.set_attr(REQUEST_PREFIX + req.token, req)

                # header, err = gs.json_encode(req.header())
                # if err:
                #     _cb_err(cb, "Failed to construct ipc header: %s" % err)
                #     continue
                #
                # body, err = gs.json_encode(arg)
                # if err:
                #     _cb_err(cb, "Failed to construct ipc body: %s" % err)
                #     continue
                #
                # ev.debug(DOMAIN, "margo request: %s " % header)
                #
                # try:
                #     # TODO (CEV): make this one object and encode it to bytes here
                #     proc.stdin.write(("%s %s\n" % (header, body)).encode('utf-8'))
                # except Exception as ex:
                #     _cb_err(cb, "Cannot talk to MarGo: %s" % ex)
                #     killSrv()
                #     gs.println(gs.traceback())

                # WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN
                # WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN
                #
                # Warning Use communicate() rather than .stdin.write, .stdout.read
                # or .stderr.read to avoid deadlocks due to any of the other OS
                # pipe buffers filling up and blocking the child process.
                #
                # WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN
                # WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN

                # ev.debug(DOMAIN, "margo request: {}".format(req.header()))
                data, err = gs.json_encode({
                    "method": req.method,
                    "token": req.token,
                    "body": arg,
                })
                if err:
                    _cb_err(cb, "Failed to construct request body (%s): %s" %
                            (req.method, err))
                    continue

                try:
                    # x = (data + "\n").encode("utf-8")
                    # print("#### DEBUG: sending request: {}".format(data))
                    # proc.stdin.write(x)
                    proc.stdin.write((data + "\n").encode("utf-8"))
                except Exception as e:
                    print("## Exception 1: {}".format(e))
                    _cb_err(cb, "Cannot talk to MarGo: %s" % e)
                    killSrv()
                    gs.println(gs.traceback())

            except Exception as e:
                print("## Exception 2: {}".format(e))
                killSrv()
                gs.println(gs.traceback())
        except Exception as e:
            print("## Exception 3: {}".format(e))
            gs.println(gs.traceback())
            break


def _call(cb, res, err):
    try:
        cb(res, err)
    except Exception:
        gs.error_traceback(DOMAIN)


def _cb_err(cb, err):
    gs.error(DOMAIN, err)
    _call(cb, {}, err)


# TODO: see if we can use asyncio for this
def _read_stdout(proc):
    """Reads lines from proc stdout into the mg9_recv_q queue.Queue, which
    is polled by _recv().
    """
    try:
        while True:
            ln = proc.stdout.readline()
            if not ln:
                break
            gs.mg9_recv_q.put(gs.ustr(ln))
    except Exception:
        gs.println(gs.traceback())

        proc.stdout.close()
        proc.wait()
        proc = None


# WARN: remove if not used
def _read_stderr(proc):
    """Reads lines from proc stderr printing them to the console
    """
    try:
        while True:
            ln = proc.stderr.readline()
            if not ln:
                break
            gs.println(ln)
    except Exception:
        gs.println(gs.traceback())

        proc.stdout.close()
        proc.wait()
        proc = None


def killSrv():
    """Kills server on process exit, registered by 'gs_init'.
    """
    p = gs.del_attr(PROC_ATTR_NAME)
    if p:
        try:
            p.stdout.close()
        except Exception:
            pass
        try:
            p.stdin.close()
        except Exception:
            pass


def on(token, cb):
    req = Request(callback=cb, token=token)
    gs.set_attr(REQUEST_PREFIX + req.token, req)


def _dump(res, err):
    gs.println(json.dumps({"res": res, "err": err}, sort_keys=True, indent=2))


# WARN: module level
#
# Start send and recieve threads.
if not gs.checked(DOMAIN, "launch ipc threads"):
    gsq.launch(DOMAIN, _send)
    gsq.launch(DOMAIN, _recv)


def on_mg_msg(res, err):
    msg = res.get("message", "")
    if msg:
        print("GoSublime: MarGo: %s" % msg)
        gs.notify("MarGo", msg)
    return True


# WARN: module level
on("margo.message", on_mg_msg)
