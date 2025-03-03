from typing import TypedDict


class App(TypedDict, total=False):
    iname: str
    name: str
    uinterval: int
    display_time: int
    notes: str
    enabled: bool
    pushed: int
    order: int
    last_render: int
    path: str
