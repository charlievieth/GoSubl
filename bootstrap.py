import os
import sublime
import sys

from logging.handlers import QueueListener
from typing import Optional

# TODO: remove this
dist_dir = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, dist_dir)

# ANN = ""
# VERSION = ""
# MARGO_EXE = ""
# fn = os.path.join(dist_dir, "gosubl", "about.py")
# execErr = ""
# try:
#     with open(fn) as f:
#         code = compile(f.read(), fn, "exec")
#         exec(code)
# except Exception:
#     execErr = "Error: failed to exec about.py: Exception: %s" % traceback.format_exc()
#     print("GoSublime: %s" % execErr)


_queue_listener: Optional[QueueListener] = None


# Called by Sublime when the plugin API is ready to use.
def plugin_loaded() -> None:
    from gosubl import about
    from gosubl import sh
    from gosubl import ev
    from gosubl import gs
    from gosubl import mg9
    from gosubl.logger import setup_queue_logger
    from gosubl.logger import log_directory

    global _queue_listener
    _queue_listener = setup_queue_logger(
        log_directory=log_directory(),
    )

    # logger = getLogger('GoSublime')
    # logger.warn("WAT WAT WAT WAT WAT WAT")

    # if VERSION != about.VERSION:
    #     gs.show_output(
    #         "GoSublime-main",
    #         "\n".join(
    #             [
    #                 "GoSublime has been updated.",
    #                 "New version: `%s`, current version: `%s`"
    #                 % (VERSION, about.VERSION),
    #                 "Please restart Sublime Text to complete the update.",
    #                 execErr,
    #             ]
    #         ),
    #     )
    #     return

    # if not about.DEVELOPMENT_MODE and gs.attr("about.version"):
    #     gs.show_output(
    #         "GoSublime-main",
    #         "\n".join(
    #             [
    #                 "GoSublime appears to have been updated.",
    #                 "New ANNOUNCE: `%s`, current ANNOUNCE: `%s`" % (ANN, about.ANN),
    #                 "You may need to restart Sublime Text.",
    #             ]
    #         ),
    #     )
    #     return

    gs.set_attr("about.version", about.VERSION)
    gs.set_attr("about.ann", about.ANN)

    for mod_name, mod in [("gs", gs), ("sh", sh), ("mg9", mg9)]:
        print("GoSublime %s: init mod(%s)" % (about.VERSION, mod_name))
        try:
            mod.gs_init({"version": about.VERSION, "ann": about.ANN, "margo_exe": about.MARGO_EXE})
        except TypeError:
            # old versions didn't take an arg
            mod.gs_init()

    ev.init.post_add = lambda e, f: f()
    ev.init()

    def cb():
        aso = gs.aso()
        old_version = aso.get("version", "")
        old_ann = aso.get("ann", "")
        if about.VERSION > old_version or about.ANN > old_ann:
            aso.set("version", about.VERSION)
            aso.set("ann", about.ANN)
            gs.save_aso()
            gs.focus(gs.dist_path("CHANGELOG.md"))

    sublime.set_timeout(cb, 0)


def plugin_unloaded() -> None:
    global _queue_listener
    try:
        if _queue_listener:
            _queue_listener.stop()
    finally:
        _queue_listener = None
