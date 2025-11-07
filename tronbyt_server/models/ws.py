"""Pydantic models for WebSocket messages."""

from typing import Literal

from pydantic import BaseModel

from tronbyt_server.models.device import DeviceInfoBase


# Client-to-server messages
class QueuedMessage(BaseModel):
    """Device has queued/buffered the image."""

    queued: int


class DisplayingMessage(BaseModel):
    """Device has started displaying the image."""

    displaying: int


class DisplayingStatusMessage(BaseModel):
    """Device has started displaying the image (alternative format)."""

    status: Literal["displaying"]
    counter: int


class ClientInfo(DeviceInfoBase):
    """Pydantic model for the client_info object from a device."""

    pass


class ClientInfoMessage(BaseModel):
    """Pydantic model for a client_info message."""

    client_info: ClientInfo


ClientMessage = (
    QueuedMessage | DisplayingMessage | DisplayingStatusMessage | ClientInfoMessage
)


# Server-to-client messages
class DwellSecsMessage(BaseModel):
    """Set the dwell time for the current image."""

    dwell_secs: int


class BrightnessMessage(BaseModel):
    """Set the display brightness."""

    brightness: int


class ImmediateMessage(BaseModel):
    """Instruct the device to display the most recently queued image immediately."""

    immediate: bool


class StatusMessage(BaseModel):
    """Inform the device of a status update."""

    status: Literal["error", "warning", "info", "debug"]
    message: str


ServerMessage = DwellSecsMessage | BrightnessMessage | ImmediateMessage | StatusMessage
