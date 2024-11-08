import re
import sublime

# GoSublime Globals

ANN = "a19.04.15-57"
VERSION = "r18.04.15-57"
VERSION_PAT = re.compile(r"\d{2}[.]\d{2}[.]\d{2}-\d+", re.IGNORECASE)
DEFAULT_GO_VERSION = "go?"
GO_VERSION_OUTPUT_PAT = re.compile(
    r"go\s+version\s+(\S+(?:\s+[+]\w+|\s+\([^)]+)?)", re.IGNORECASE
)
GO_VERSION_NORM_PAT = re.compile(r"[^\w.+-]+", re.IGNORECASE)
PLATFORM = "{}-{}".format(sublime.platform(), sublime.arch())
MARGO_EXE_PREFIX = "gosublime.margo_"
MARGO_EXE_SUFFIX = ".exe"
MARGO_EXE = MARGO_EXE_PREFIX + VERSION + "_" + DEFAULT_GO_VERSION + MARGO_EXE_SUFFIX
MARGO_EXE_PAT = re.compile(r"^gosublime\.margo.*\.exe$", re.IGNORECASE)

# CEV: Dev Globals

FORCE_INSTALL = False
MARGO_PPROF_ADDR = None
# MARGO_PPROF_ADDR = ':6061'

# Don't print "GoSublime appears to have been updated." on reload
DEVELOPMENT_MODE = True
