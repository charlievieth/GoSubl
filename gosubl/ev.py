import threading
import traceback


class Event(object):
    def __init__(self):
        self.lck = threading.Lock()

        # Function list: signature f(*args, **kwargs).  Each function is
        # called with the arguments to __call__.
        self.lst = []

        # Function called whenever an item is appended to the Event via
        # __iadd__.  The signature is post_add(self, f).
        self.post_add = None

    def __call__(self, *args, **kwargs):
        """Called when Event is "called" as a function.
        """
        with self.lck:
            l = self.lst[:]

        for f in l:
            try:
                f(*args, **kwargs)
            except Exception:
                print(traceback.format_exc())

        return self

    def __iadd__(self, f):
        """Override the '+=' operator, appending f to self.lst.
        """
        with self.lck:
            self.lst.append(f)

        if self.post_add:
            try:
                self.post_add(self, f)
            except Exception:
                print(traceback.format_exc())

        return self

    def __isub__(self, f):
        """Override the '-=' operator, removing f from self.lst.
        """
        with self.lck:
            self.lst.remove(f)

        return self

    def __len__(self):
        """Override len() to return the length of the Event's list lst.
        """
        with self.lck:
            return len(self.lst)


# TODO: Figure out how these are used.

# CEV: Called from mg9.py and sh.py.
#
# It appears that by setting from handlers (loggers) to Event.lst this can
# be used for debugging.
debug = Event()

# CEV: Appears to only be used by GoSublime.py
init = Event()
