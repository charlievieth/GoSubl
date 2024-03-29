# Copyright (C) 2013 ~ 2016 - Oscar Campos <oscar.campos@member.fsf.org>
# This program is Free Software see LICENSE file for details

from time import sleep
from typing import List
from typing import Optional

import sublime
from Default.history_list import get_jump_history_for_view

from gosubl.utils import view_file_name
from gosubl.utils import view_is_loaded


class ExplorerPanel:
    """
    Creates a panel that can be used to explore nested options sets

    The data structure for the options is as follows:

        Options[
            {
                'title': 'Title Data'
                'details': 'Details Data',
                'location': 'File: {} Line: {} Column: {}',
                'position': 'filepath:line:col',
                'options': [
                    {
                        'title': 'Title Data'
                        'details': 'Details Data',
                        'location': 'File: {} Line: {} Column: {}',
                        'position': 'filepath:line:col',
                        'options': [
                        ]...
                    }
                ]
            }
        ]

    So we can nest as many levels as we want

    NB (CEV): the format of usages/options is (Anaconda/commands/find_usages.py):

        usages.append({
            'title': usage[0],
            'location': 'File: {} Line: {} Column: {}'.format(
                usage[1], usage[2], usage[3]
            ),
            'position': '{}:{}:{}'.format(usage[1], usage[2], usage[3])
        })
    """

    def __init__(self, view: sublime.View, options: List) -> None:
        self.options = options
        self.view = view
        self.selected = []  # type: List
        self.restore_point = view.sel()[0]

    def show(self, cluster: List, forced: bool = False) -> None:
        """Show the quick panel with the given options"""

        if not cluster:
            cluster = self.options

        if len(cluster) == 1 and not forced:
            try:
                Jumper(self.view, cluster[0]['position']).jump()
            except KeyError:
                if len(cluster[0].get('options', [])) == 1 and not forced:
                    Jumper(self.view, cluster[0]['options'][0]['position']).jump()
            return

        self.last_cluster = cluster
        quick_panel_options = []
        for data in cluster:
            tmp = [data['title']]
            if 'details' in data:
                tmp.append(data['details'])
            if 'location' in data:
                tmp.append(data['location'])
            quick_panel_options.append(tmp)

        self.view.window().show_quick_panel(
            quick_panel_options,
            on_select=self.on_select,
            on_highlight=lambda index: self.on_select(index, True),
        )

    def on_select(self, index: int, transient: bool = False) -> None:
        """Called when an option is been made in the quick panel"""

        if index == -1:
            self._restore_view()
            return

        cluster = self.last_cluster
        node = cluster[index]
        if transient and 'options' in node:
            return

        if 'options' in node:
            self.prev_cluster = self.last_cluster
            opts = node['options'][:]
            opts.insert(0, {'title': '<- Go Back', 'position': 'back'})
            sublime.set_timeout(lambda: self.show(opts), 0)
        else:
            if node['position'] == 'back' and not transient:
                sublime.set_timeout(lambda: self.show(self.prev_cluster), 0)
            elif node['position'] != 'back':
                Jumper(self.view, node['position']).jump(transient)

    def _restore_view(self):
        """Restore the view and location"""

        sublime.active_window().focus_view(self.view)
        self.view.show(self.restore_point)

        if self.view.sel()[0] != self.restore_point:
            self.view.sel().clear()
            self.view.sel().add(self.restore_point)


class Jumper:
    """Jump to the specified file line and column making an indicator to toggle"""

    __slots__ = "position", "view", "new_view", "force_go_syntax"

    def __init__(
        self,
        view: sublime.View,
        position: str,
        force_go_syntax: bool = False,
    ) -> None:
        # CEV: position is: "File:Line:Column"
        self.position = position
        self.view = view
        self.new_view: Optional[sublime.View] = None
        self.force_go_syntax = force_go_syntax

    def jump(self, transient: bool = False) -> None:
        """Jump to the selection"""

        flags = sublime.ENCODED_POSITION
        if transient is True:
            flags |= sublime.TRANSIENT

        get_jump_history_for_view(self.view).push_selection(self.view)
        self.new_view = sublime.active_window().open_file(
            self.position,
            flags,
        )
        if self.new_view:
            if self.force_go_syntax:
                sublime.set_timeout_async(self._set_go_syntax, 0)
            if not transient:
                sublime.set_timeout_async(self._toggle_indicator, 0)

    def _set_go_syntax(self) -> None:
        # WARN: load "gs" here because Sublime loads files by name and
        # GoSublime.py is required to make "gosubl" importable.
        from gosubl import gs

        view = self.new_view
        if view and view_is_loaded(view) and not view_file_name(view).endswith(".go"):
            syntax = view.syntax()
            go_syntax = gs.tm_path("go")
            if syntax and syntax.path != go_syntax:
                view.set_syntax_file(go_syntax)

    def _toggle_indicator(self) -> None:
        """Toggle mark indicator to focus the cursor"""

        view = self.new_view
        if view is None:
            return

        path, line, column = self.position.rsplit(':', 2)
        if not view_is_loaded(view):
            view = sublime.active_window().find_open_file(path)
            if view is None:
                return

        pt = view.text_point(int(line) - 1, int(column))
        region_name = 'gosubl.indicator.{}.{}'.format(view.id(), line)

        for i in range(3):
            delta = 300 * i * 2
            sublime.set_timeout(
                lambda: view.add_regions(
                    region_name,
                    [sublime.Region(pt, pt)],
                    'comment',
                    'bookmark',
                    sublime.DRAW_EMPTY_AS_OVERWRITE,
                ),
                delta,
            )
            sublime.set_timeout(
                lambda: view.erase_regions(region_name), delta + 300
            )
