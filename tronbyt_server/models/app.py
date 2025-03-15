from typing import Any, Dict, List, Required, TypedDict


class App(TypedDict, total=False):
    iname: Required[str]
    name: Required[str]
    uinterval: int
    display_time: int
    notes: str
    enabled: bool
    pushed: int
    order: int
    last_render: int
    path: str
    start_time: str
    end_time: str
    days: List[str]
    config: Dict[str, Any]
