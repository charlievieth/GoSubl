import re

from datetime import datetime
from logging import getLogger
from os.path import basename
from os.path import dirname
from typing import Any
from typing import Callable
from typing import Dict
from typing import List
from typing import Optional
from typing import Set
from typing import Tuple
from typing import TypedDict

import sublime
import sublime_plugin

from gosubl import gs
from gosubl import mg9
from gosubl.utils import first_selection_region
from gosubl.utils import LRUCache
from gosubl.utils import view_file_name

logger = getLogger('GsComplete')

AC_OPTS = sublime.INHIBIT_WORD_COMPLETIONS | sublime.INHIBIT_EXPLICIT_COMPLETIONS
REASONABLE_PKGNAME_PAT = re.compile(r"^\w+$")

END_SELECTOR_PAT = re.compile(r".*?((?:[\w.]+\.)?(\w+))$")
START_SELECTOR_PAT = re.compile(r"^([\w.]+)")
DOMAIN = "GsComplete"
SNIPPET_VAR_PAT = re.compile(r"\$\{([a-zA-Z]\w*)\}")

HINT_KEY = "%s.completion-hint" % DOMAIN

CLASS_PREFIXES = {
    "const": u"\u0196",  # Ɩ
    "func": u"\u0192",   # ƒ
    "type": u"\u0288",   # ʈ
    "var": u"\u03BD",    # ν
    "package": u"package \u03C1",  # package ρ
}

# TODO: what does this do?
NAME_PREFIXES = {"interface": u"\u00A1"}  # ¡

# Push the cursor back the selector/dot (.) when completing so that all
# possible results are shown. Set this to True to restore the prior behavior.
COMPLETE_FROM_SELECTOR = False

GO_TOKENS = frozenset(
    [
        "=",
        "]",
        "*",
        "^",
        "&",
        "<",
        ")",
        "+",
        "%",
        ":",
        "|",
        "[",
        ",",
        "-",
        ">",
        "/",
        "}",
        "(",
        ";",
        ".",
        "{",
        "!",
        "\"",
        "'",
        " ",
        "\n",
        "\t",
        "\v",
        "\r",
    ]
)

# TODO: move these somewhere else
SnippetContext = TypedDict("SnippetContext", {
    "global": bool,
    "local": bool,
    "pkgname": str,
    "types": List[str],
    "has_types": bool,  # Why do we need this?
    "default_pkgname": str,
    "fn": str,
})


SnippetMatch = TypedDict("SnippetMatch", {
    "global": Optional[bool],
    "local": Optional[bool],
    "pkgname": Optional[str],
    "has_types": Optional[bool],
})


class SnippetValue(TypedDict):
    text: str
    title: str
    value: str


class GosublimeSnippet(TypedDict):
    match: SnippetMatch
    snippets: List[SnippetValue]


AutoCompleteFlags = int
CompletionValue = Tuple[str, str]  # TODO: return a CompletionItem

# Tuple[view.id(), view.change_id()[0], pos]
CalltipCacheKey = Tuple[int, int, int]

# Cache calltips
calltip_cache: Dict[CalltipCacheKey, mg9.CompleteResponse] = LRUCache(maxsize=128)


def calltip_cache_key(view: sublime.View, source: str, cursor: int) -> CalltipCacheKey:
    # Move cursor to the end of the symbol to improve cache performance
    for i in range(cursor, len(source)):
        c = source[i]
        if not c.isalnum() and c != '_':
            if i > cursor:
                cursor = i - 1
                break

    return (view.id(), view.change_count(), cursor)


def snippet_match(ctx: SnippetContext, m: GosublimeSnippet) -> bool:
    try:
        for k, p in m.get("match", {}).items():
            q = ctx.get(k, "")
            if isinstance(p, str) and p:
                if not re.search(p, str(q)):
                    return False
            elif p != q:
                return False
    except:
        # WARN: return False here ???
        gs.notice(DOMAIN, gs.traceback())
    return True


def expand_snippet_vars(
    vars: Dict[str, str],
    text: str,
    title: str,
    value: str,
) -> Tuple[str, str, str]:
    def sub(m: re.Match) -> str:
        return vars.get(m.group(1), "")
    return (
        SNIPPET_VAR_PAT.sub(sub, text),
        SNIPPET_VAR_PAT.sub(sub, title),
        SNIPPET_VAR_PAT.sub(sub, value),
    )


def method_receiver_name(type_name: str) -> str:
    # This determines the receiver name and changes "cX" => "x" and "Cx" => "c".
    if type_name:
        if (
            len(type_name) >= 2 and type_name[0].islower() and
            type_name[1].isupper()
        ):
            return type_name[1].lower()
        else:
            return type_name[0].lower()
    else:
        return ""


def resolve_snippets(ctx: SnippetContext) -> List[Tuple[str, str]]:
    if ctx.get("local") is True:
        types = [""]
    else:
        types = ctx.get("types", [""])

    variables = {k: v for k, v in ctx.items() if isinstance(v, str)}
    cl: Set[Tuple[str, str]] = set()

    snips: List[GosublimeSnippet] = []
    for extra in [gs.setting("default_snippets"), gs.setting("snippets")]:
        if extra:
            snips.extend(extra)
    for m in snips:
        try:
            if snippet_match(ctx, m):
                for ent in m.get("snippets", []):
                    text = ent.get("text", "")
                    value = ent.get("value", "")
                    if text and value:
                        for type_name in types:
                            variables["typename"] = type_name
                            variables["typename_abbr"] = method_receiver_name(
                                type_name,
                            )
                            title = ent.get("title", "")
                            txt, ttl, val = expand_snippet_vars(
                                variables, text, title, value
                            )
                            s = u"%s\t%s \u0282" % (txt, ttl)
                            cl.add((s, val))
        except:
            gs.notice(DOMAIN, gs.traceback())
    return list(cl)


class GoSublime(sublime_plugin.EventListener):
    def on_query_completions(
        self,
        view: sublime.View,
        prefix: str,
        locations: List[int]
    ) -> Optional[Tuple[List[CompletionValue], AutoCompleteFlags]]:

        if gs.setting("gscomplete_enabled", False) is not True:
            return None

        start = datetime.now()

        pos = locations[0]
        scopes = view.scope_name(pos).split()
        if "source.go" not in scopes:
            return None

        # TODO: is there a better way to do this?
        if not gs.IGNORED_SCOPES.isdisjoint(scopes):
            return ([], AC_OPTS)

        # TODO: this only selects types from the current file (which is
        # probably fine), but we should move this to margo / gocode.
        types = []
        for r in view.find_by_selector("source.go entity.name.type.go"):
            types.append(view.substr(r).lstrip())

        # TODO: move package name completion to margo / gocode
        # or at least be lazy about it.
        file_name = view_file_name(view)
        try:
            if basename(file_name) == "main.go":
                default_pkgname = "main"
            else:
                default_pkgname = basename(dirname(file_name))
        except Exception:
            default_pkgname = ""

        if not REASONABLE_PKGNAME_PAT.match(default_pkgname):
            default_pkgname = ""

        r = view.find(r"^package[ \t]+([a-zA-Z]\w*)", 0)
        pkgname = view.substr(view.word(r.end())) if r else ""

        if not default_pkgname:
            default_pkgname = pkgname if pkgname else "main"

        ctx: SnippetContext = {
            "global": bool(pkgname and pos > view.line(r).end()),
            "local": False,
            "pkgname": pkgname,
            "types": types or [""],
            "has_types": bool(len(types) > 0),
            "default_pkgname": default_pkgname,
            "fn": file_name or "",
        }
        show_snippets = gs.setting("autocomplete_snippets", True) is True

        # TODO: move snippets to syntax files
        if not pkgname:
            return (resolve_snippets(ctx), AC_OPTS) if show_snippets else ([], AC_OPTS)

        # Push cursor back to selector to show all possible completions
        # instead of only showing what matches the prefix.
        if COMPLETE_FROM_SELECTOR:
            offset = pos - len(prefix)
        else:
            offset = pos

        src = view.substr(sublime.Region(0, view.size()))

        fn = file_name or "<stdin>"
        if not src:
            return ([], AC_OPTS)

        pre_complete = datetime.now()
        nc = view.substr(sublime.Region(pos, pos + 1))
        cl = self.complete(fn, offset, src, nc.isalpha() or nc == "(")

        end_complete = datetime.now()

        pc = view.substr(sublime.Region(pos - 1, pos))
        if show_snippets and (pc.isspace() or pc.isalpha()):
            if scopes[-1] == "source.go":
                cl.extend(resolve_snippets(ctx))
            elif scopes[-1] == "meta.block.go" and (
                "meta.function.plain.go" in scopes or
                "meta.function.receiver.go" in scopes
            ):
                ctx["global"] = False
                ctx["local"] = True
                cl.extend(resolve_snippets(ctx))

        end = datetime.now()

        dur = (end - start).total_seconds() * 1000
        pre_dur = (pre_complete - start).total_seconds() * 1000
        comp_dur = (end_complete - pre_complete).total_seconds() * 1000
        logger.warn(
            f"complete: total: {dur:.2f}ms pre: {pre_dur:.2f}ms comp: {comp_dur:.2f}ms"
        )
        return (cl, AC_OPTS)

    def autocomplete_filter(self) -> Optional[re.Pattern]:
        pattern = gs.setting("autocomplete_filter_name")
        if pattern:
            try:
                return re.compile(pattern)
            except Exception as ex:
                gs.notice(
                    f"invalid \"autocomplete_filter_name\" pattern: {pattern}: {ex}",
                )
        return None

    def complete(
        self,
        fn: str,
        offset: int,
        src: str,
        func_name_only: bool,
    ) -> List[Tuple[str, str]]:
        """Go completion via gocode (called from mg9.py)."""
        comps = []
        autocomplete_tests = gs.setting("autocomplete_tests", False)
        autocomplete_closures = gs.setting("autocomplete_closures", False)
        ents, err = mg9.complete(fn, src, offset)
        if err:
            gs.notice(DOMAIN, err)

        name_fx = self.autocomplete_filter()

        # TODO: Remove Regexes and move to Go.
        for ent in ents:
            if name_fx and name_fx.search(ent["name"]):
                continue

            tn = ent.get("type", "")
            cn = ent["class"]
            nm = ent["name"]
            is_func = cn == "func"
            is_func_type = cn == "type" and tn.startswith("func(")

            if is_func:
                if nm in ("main", "init"):
                    continue

                if not autocomplete_tests and nm.startswith(
                    ("Test", "Benchmark", "Example")
                ):
                    continue

            if is_func or is_func_type:
                s_sfx = u"\u0282"  # ʂ
                t_sfx = CLASS_PREFIXES.get("type", "")
                f_sfx = CLASS_PREFIXES.get("func", "")
                params, ret = declex(tn)
                decls = []
                for i, p in enumerate(params):
                    n, t = p
                    if t.startswith("..."):
                        n = "..."
                    decls.append("${%d:%s}" % (i + 1, n))
                param_fields = ", ".join(decls)
                ret = ret.strip("() ")

                if is_func:
                    if func_name_only:
                        comps.append(("%s\t%s %s" % (nm, ret, f_sfx), nm))
                    else:
                        comps.append(
                            ("%s\t%s %s" % (nm, ret, f_sfx), "%s(%s)" % (nm, param_fields))
                        )
                else:
                    comps.append(("%s\t%s %s" % (nm, tn, t_sfx), nm))

                    # WARN: I don't this this works
                    if autocomplete_closures:
                        comps.append(
                            (
                                "%s {}\tfunc() {...} %s" % (nm, s_sfx),
                                "%s {\n\t${0}\n}" % tn,
                            )
                        )
            elif cn != "PANIC":
                comps.append(
                    ("%s\t%s %s" % (nm, tn, self.typeclass_prefix(cn, tn)), nm)
                )
        return comps

    def typeclass_prefix(self, typeclass: str, typename: str) -> str:
        if typename == "interface" or typename == "any":
            return "¡"
        else:
            if typeclass == "const":
                return "Ɩ"
            elif typeclass == "func":
                return "ƒ"
            elif typeclass == "type":
                return "ʈ"
            elif typeclass == "var":
                return "ν"
            elif typeclass == "package":
                return "package ρ"
            else:
                return ""


def declex(s: str) -> Tuple[List[Tuple[str, str]], str]:
    # declex creates a list of function arguments and types:
    #
    # "func(w io.Writer, candidates []Candidate, num int)" =>
    #   ([('w', 'io.Writer'), ('candidates', '[]Candidate'), ('num', 'int')], )
    #
    params = []
    ret = ""
    if s.startswith("func("):
        lp = len(s)
        sp = 5
        ep = sp
        dc = 1
        names: List[str] = []
        while ep < lp and dc > 0:
            c = s[ep]
            if dc == 1 and c in (",", ")"):
                if sp < ep:
                    n, _, t = s[sp:ep].strip().partition(" ")
                    t = t.strip()
                    if t:
                        for name in names:
                            params.append((name, t))
                        names = []
                        params.append((n, t))
                    else:
                        names.append(n)
                    sp = ep + 1
            if c == "(":
                dc += 1
            elif c == ")":
                dc -= 1
            ep += 1
        ret = s[ep:].strip() if ep < lp else ""
    return (params, ret)


def _ct_poller() -> None:
    try:
        if gs.setting("calltips") is True:
            view = sublime.active_window().active_view()
            if view is not None and not view.is_loading():
                view.run_command("gs_show_call_tip", {"set_status": True})
        else:
            view.erase_status(HINT_KEY)
    except Exception:
        pass

    sublime.set_timeout(_ct_poller, 1000)


# WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN
#
# THIS IS BROKEN
#
# WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN WARN
class GsShowCallTip(sublime_plugin.TextCommand):

    def is_enabled(self) -> bool:
        return gs.is_go_source_view(self.view)

    def _xrun(self, edit: sublime.Edit, set_status: bool = False) -> None:
        view = self.view
        # logger.warn(f"calltip: view_id: {view.id()} buffer_id: {view.buffer_id()} change_id: {view.change_id()[0]}")

        fn = view.file_name()
        src = gs.view_src(view)
        pos = gs.sel(view).begin()

        if pos < 0 or pos >= len(src) or src[pos] in GO_TOKENS:
            view.erase_status(HINT_KEY)
            return

        def _show_output(self, msg: str) -> None:
            gs.show_output(HINT_KEY, msg, print_output=False, syntax_file="GsDoc")

        def f(cl: List[mg9.CompleteCandidate], err: Optional[str]) -> None:
            def f2(cl: List[mg9.CompleteCandidate], err: Optional[str]) -> None:
                c: mg9.CompleteResponse = {}
                if len(cl) == 1:
                    c = cl[0]
                else:
                    logger.warn(f"complete candidates {len(cl)}: {cl}")
                    pass

                if set_status:
                    if c:
                        pfx = "func("
                        typ = c["type"]
                        if typ.startswith(pfx):
                            s = "func %s(%s" % (c["name"], typ[len(pfx):])
                        else:
                            s = "%s: %s" % (c["name"], typ)

                        view.set_status(HINT_KEY, s)
                    else:
                        view.erase_status(HINT_KEY)
                else:
                    if c:
                        s = "%s %s\n%s" % (c["name"], c["class"], c["type"])
                    else:
                        s = "// %s" % (err or "No calltips found")

                    gs.show_output(HINT_KEY, s, print_output=False, syntax_file="GsDoc")

            sublime.set_timeout(lambda: f2(cl, err), 0)

        mg9.calltip(fn, src, pos, set_status, f)

    def update_status(
        self,
        cl: List[mg9.CompleteCandidate],
        err: Optional[str],
        set_status: bool = False,
    ) -> None:
        # TODO: can we make this work if there are multiple candidates?
        if not cl or len(cl) != 1:
            if set_status:
                self.view.erase_status(HINT_KEY)
            else:
                self._show_output("// " + err if err else "No calltips found")
            return

        c = cl[0]
        if set_status:
            pfx = "func("
            typ = c["type"]
            if typ.startswith(pfx):
                s = "func %s(%s" % (c["name"], typ[len(pfx):])
            else:
                s = "%s: %s" % (c["name"], typ)

            self.view.set_status(HINT_KEY, s)
        else:
            var_type = c.get("type")
            if var_type:
                msg = f"{c['name']} {c['class']}\n{var_type}"
            else:
                msg = f"{c['name']} {c['class']}"
            self._show_output(msg)

    def run(self, edit: sublime.Edit, set_status: bool = False) -> None:
        view = self.view
        if view is None or view.is_loading():
            view.erase_status(HINT_KEY)
            return

        # logger.warn(f"calltip: view_id: {view.id()} buffer_id: {view.buffer_id()} change_id: {view.change_count()}")

        source = gs.view_src(view)
        cursor = gs.sel(view).begin()
        if cursor < 0 or cursor >= len(source) or source[cursor] in GO_TOKENS:
            view.erase_status(HINT_KEY)
            return

        cache_key = calltip_cache_key(view, source, cursor)

        if cache_key in calltip_cache:
            # logger.warning("calltips: cache hit")
            cl, err = calltip_cache[cache_key]
            self.update_status(cl, err, set_status)
            return

        # Query margo for the calltip
        def calltip_cb(cl: List[mg9.CompleteCandidate], err: Optional[str]) -> None:
            # logger.warning(f"calltips: cache miss: {cl}")
            calltip_cache[cache_key] = (cl, err)
            sublime.set_timeout(
                lambda: self.update_status(cl, err, set_status), 0
            )

        file_name = view.file_name()
        mg9.calltip(file_name, source, cursor, set_status, calltip_cb)


def debounced(f: Callable[[], Any], timeout_ms: int = 0, condition: Callable[[], bool] = lambda: True,
              async_thread: bool = False) -> None:
    """
    Possibly run a function at a later point in time, either on the async thread or on the main thread.

    :param      f:             The function to possibly run. Its return type is discarded.
    :param      timeout_ms:    The time in milliseconds after which to possibly to run the function
    :param      condition:     The condition that must evaluate to True in order to run the funtion
    :param      async_thread:  If true, run the function on the async worker thread, otherwise run the function on the
                               main thread
    """

    def run() -> None:
        if condition():
            f()

    runner = sublime.set_timeout_async if async_thread else sublime.set_timeout
    runner(run, timeout_ms)


CALLTIP_TIMEOUT = 200  # milliseconds


# TODO:
#   * Implement: on_hover()
class GsShowCallTipX(sublime_plugin.ViewEventListener):

    calltip_debounce_time = CALLTIP_TIMEOUT

    @classmethod
    def applies_to_primary_view_only(cls) -> bool:
        return False

    @classmethod
    def is_applicable(cls, settings: sublime.Settings) -> bool:
        return settings.get("syntax", "").endswith("Go.sublime-syntax")

    # TODO: watch for changes to settings.get("syntax") and use this
    # to short circuit on_selection_modified_async().
    def __init__(self, view: sublime.View) -> None:
        super().__init__(view)
        self._stored_region = sublime.Region(-1, -1)

    def on_selection_modified_async(self) -> None:
        if not gs.is_go_source_view(self.view):
            return

        # logger.warn(f"settings: {self.view.settings().to_dict()}")

        # TODO: check `gs.setting("calltips")`
        different, current_region = self._update_stored_region_async()
        if different:
            self._when_selection_remains_stable_async(
                self._do_calltip_async,
                current_region,
                after_ms=self.calltip_debounce_time,
            )

    def _clear_calltip_hint(self) -> None:
        self.view.erase_status(HINT_KEY)

    def _do_calltip_async(self) -> None:
        source = gs.view_src(self.view)
        cursor = gs.sel(self.view).begin()
        if cursor < 0 or cursor >= len(source) or source[cursor] in GO_TOKENS:
            self._clear_calltip_hint()
            return

        cache_key = calltip_cache_key(self.view, source, cursor)

        if cache_key in calltip_cache:
            logger.warning("calltips: cache hit")
            cl, err = calltip_cache[cache_key]
            self._update_calltip_status_async(cl, err)
            return

        # Query margo for the calltip
        def calltip_cb(cl: List[mg9.CompleteCandidate], err: Optional[str]) -> None:
            logger.warning(f"calltips: cache miss: {cache_key}: {cl}")
            calltip_cache[cache_key] = (cl, err)
            sublime.set_timeout(
                lambda: self._update_calltip_status_async(cl, err), 0
            )

        file_name = self.view.file_name()
        mg9.calltip(file_name, source, cursor, False, calltip_cb)

    # def _trim_prefix(s: str, prefix: str) -> str:
    #     return s[len(prefix):] if s.startswith(prefix) else s

    def _update_calltip_status_async(
        self,
        candidates: List[mg9.CompleteCandidate],
        err: Optional[str],
    ) -> None:
        # TODO: can we make this work if there are multiple candidates?
        if not candidates or len(candidates) != 1:
            self._clear_calltip_hint()
        else:
            c = candidates[0]
            pfx = "func("
            typ = c["type"]
            if typ.startswith(pfx):
                s = "func %s(%s" % (c["name"], typ[len(pfx):])
            else:
                s = "%s: %s" % (c["name"], typ)
            self.view.set_status(HINT_KEY, s)

    def _update_stored_region_async(self) -> Tuple[bool, sublime.Region]:
        """
        Stores the current first selection in a variable.
        Note that due to this function (supposedly) running in the async worker thread of ST, it can happen that the
        view is already closed. In that case it returns Region(-1, -1). It also returns that value if there's no first
        selection.

        :returns:   A tuple with two elements. The second element is the new region, the first element signals whether
                    the previous region was different from the newly stored region.
        """
        current_region = first_selection_region(self.view)
        if current_region is not None:
            if self._stored_region != current_region:
                self._stored_region = current_region
                return True, current_region
        return False, sublime.Region(-1, -1)

    def _when_selection_remains_stable_async(
        self,
        f: Callable[[], None],
        r: sublime.Region,
        after_ms: int,
    ) -> None:
        debounced(f, after_ms, lambda: self._stored_region == r, async_thread=True)


# if not gs.checked(DOMAIN, "_ct_poller"):
#     _ct_poller()
