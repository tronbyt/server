from typing import Any, Dict, List, Optional, Required, TypedDict


class App(TypedDict, total=False):
    id: str
    iname: Required[str]
    name: Required[str]
    uinterval: int
    display_time: int
    notes: str
    enabled: bool
    pushed: bool
    order: int
    last_render: int
    path: str
    start_time: str
    end_time: str
    days: List[str]
    config: Dict[str, Any]
    empty_last_render: bool
    render_messages: List[str]  # Changed from str to List[str]


class AppMetadata(TypedDict, total=False):
    id: str
    name: str
    summary: str
    desc: str
    author: str
    path: str
    fileName: Optional[str]
    packageName: Optional[str]
    preview: Optional[str]
    supports2x: bool
    recommended_interval: int
