"""Websockets router."""

import asyncio
import logging
import sqlite3
from dataclasses import dataclass
from pathlib import Path
from datetime import datetime, timezone
from typing import Any, cast

from fastapi import APIRouter, Depends, WebSocket, status, Response
from fastapi.responses import FileResponse
from pydantic import TypeAdapter, ValidationError
from starlette.websockets import WebSocketDisconnect

import re
from tronbyt_server import db
from tronbyt_server.dependencies import get_db
from tronbyt_server.models import (
    BrightnessMessage,
    ClientInfoMessage,
    ClientMessage,
    Device,
    DisplayingMessage,
    DisplayingStatusMessage,
    DwellSecsMessage,
    ImmediateMessage,
    ProtocolType,
    QueuedMessage,
    ServerMessage,
    StatusMessage,
    User,
)
from tronbyt_server.routers.manager import next_app_logic
from tronbyt_server.models.sync import SyncPayload
from tronbyt_server.sync import get_sync_manager

router = APIRouter(tags=["websockets"])
logger = logging.getLogger(__name__)

server_message_adapter: TypeAdapter[ServerMessage] = TypeAdapter(ServerMessage)


async def _send_message(websocket: WebSocket, message: ServerMessage) -> None:
    """Send a message to the websocket."""
    await websocket.send_text(server_message_adapter.dump_json(message).decode("utf-8"))


class DeviceAcknowledgment:
    """Manages device acknowledgment state for queued/displaying messages."""

    def __init__(self) -> None:
        """Initializes the DeviceAcknowledgment."""
        self.queued_event = asyncio.Event()
        self.displaying_event = asyncio.Event()
        self.queued_counter: int | None = None
        self.displaying_counter: int | None = None
        self.brightness_to_send: int | None = None  # If set, send this brightness value

    def reset(self) -> None:
        """Reset events for next image."""
        self.queued_event.clear()
        self.displaying_event.clear()
        self.queued_counter = None
        self.displaying_counter = None
        # Don't reset brightness_to_send - it should persist until sent

    def mark_queued(self, counter: int) -> None:
        """Mark image as queued by device."""
        self.queued_counter = counter
        self.queued_event.set()

    def mark_displaying(self, counter: int) -> None:
        """Mark image as displaying by device."""
        self.displaying_counter = counter
        self.displaying_event.set()


@dataclass
class Connection:
    """Represents an active WebSocket connection and its associated tasks."""

    websocket: WebSocket
    ack: "DeviceAcknowledgment"
    sender_task: asyncio.Task[None]
    receiver_task: asyncio.Task[None]


class ConnectionManager:
    """Manages active WebSocket connections."""

    def __init__(self) -> None:
        self._active_connections: dict[str, "Connection"] = {}
        self._lock = asyncio.Lock()

    async def register(
        self,
        device_id: str,
        websocket: WebSocket,
        ack: "DeviceAcknowledgment",
        sender_task: asyncio.Task[None],
        receiver_task: asyncio.Task[None],
    ) -> None:
        """Register a new connection, cleaning up any existing one."""
        new_connection = Connection(
            websocket=websocket,
            ack=ack,
            sender_task=sender_task,
            receiver_task=receiver_task,
        )

        old_connection: "Connection | None"
        async with self._lock:
            old_connection = self._active_connections.get(device_id)
            self._active_connections[device_id] = new_connection

        logger.info(f"[{device_id}] WebSocket connection registered")

        if old_connection:
            logger.warning(f"[{device_id}] Existing connection found, cleaning up.")
            old_connection.sender_task.cancel()
            old_connection.receiver_task.cancel()
            # Wait for the old tasks to finish their cleanup.
            await asyncio.gather(
                old_connection.sender_task,
                old_connection.receiver_task,
                return_exceptions=True,
            )

    async def unregister(self, device_id: str, websocket: WebSocket) -> None:
        """Unregister a connection if it's the current one."""
        # This check is atomic and safe.
        async with self._lock:
            if (
                connection := self._active_connections.get(device_id)
            ) and connection.websocket is websocket:
                self._active_connections.pop(device_id)
                logger.info(f"[{device_id}] WebSocket connection unregistered")

    async def get_ack(self, device_id: str) -> "DeviceAcknowledgment | None":
        """Get the ack object for a given device_id atomically."""
        async with self._lock:
            connection = self._active_connections.get(device_id)
            if connection:
                return connection.ack
            return None

    async def get_websocket(self, device_id: str) -> WebSocket | None:
        """Get the websocket for a given device_id atomically."""
        async with self._lock:
            connection = self._active_connections.get(device_id)
            if connection:
                return connection.websocket
            return None


# Global instance of the connection manager
connection_manager = ConnectionManager()


async def send_brightness_update(device_id: str, brightness: int) -> None:
    """Send a brightness update to an active websocket connection."""
    get_sync_manager().notify(device_id, SyncPayload(payload=brightness))


async def _send_response(
    websocket: WebSocket, response: Response, last_brightness: int
) -> tuple[int, int]:
    """Send a response to the websocket.

    Returns: (dwell_time, last_brightness)
    """
    dwell_time = 5
    if response.status_code == 200:
        # Check if this should be displayed immediately (interrupting current display)
        immediate = response.headers.get("Tronbyt-Immediate")

        # Get the dwell time from the response header
        dwell_secs = response.headers.get("Tronbyt-Dwell-Secs")
        if dwell_secs:
            dwell_time = int(dwell_secs)

        logger.debug(f"Sending dwell_secs to device: {dwell_time}s")
        await _send_message(websocket, DwellSecsMessage(dwell_secs=dwell_time))

        # Update confirmation timeout now that we have the actual dwell time
        # confirmation_timeout = dwell_time

        # Send brightness as a text message, if it has changed
        # This must be done before sending the image so that the new value is applied to the next image
        brightness_str = response.headers.get("Tronbyt-Brightness")
        if brightness_str:
            brightness = int(brightness_str)
            if brightness != last_brightness:
                await _send_message(websocket, BrightnessMessage(brightness=brightness))
                last_brightness = brightness

        # Send the image as a binary message FIRST
        # This allows the device to queue/buffer the image before being told to interrupt
        if isinstance(response, FileResponse):
            content = await asyncio.get_running_loop().run_in_executor(
                None, Path(response.path).read_bytes
            )
            await websocket.send_bytes(content)
        else:
            await websocket.send_bytes(cast(bytes, response.body))

        # Send immediate flag AFTER image bytes so device can queue the image first
        # Then immediately interrupt and display it
        if immediate:
            logger.debug("Sending immediate display flag to device AFTER image bytes")
            await _send_message(websocket, ImmediateMessage(immediate=True))

        dwell_time = int(response.headers.get("Tronbyt-Dwell-Secs", 5))
    else:
        await _send_message(
            websocket,
            StatusMessage(
                status="error",
                message=f"Error fetching image: {response.status_code}",
            ),
        )
    return dwell_time, last_brightness


async def _wait_for_acknowledgment(
    ack: DeviceAcknowledgment,
    dwell_time: int,
    db_conn: sqlite3.Connection,
    loop: asyncio.AbstractEventLoop,
    waiter: Any,  # Waiter (using Any to avoid mypy issues with abstract base class)
    websocket: WebSocket,
    last_brightness: int,
    user: User,
    device: Device,
) -> tuple[Response, int]:
    """Wait for device to acknowledge displaying the image, with timeout and ephemeral push detection.

    Returns tuple of (next Response to send, updated last_brightness).
    """
    poll_interval = 1  # Check every second
    time_waited = 0

    # Determine timeout based on firmware type
    if device.info.protocol_version is None:
        # Old firmware doesn't send messages, just wait for dwell_time
        extended_timeout = dwell_time
        logger.debug(
            f"[{device.id}] Using old firmware timeout of {extended_timeout}s (dwell_time)"
        )
    else:
        # New firmware - give device full dwell time + buffer
        # Use 2x dwell time to give plenty of room for the device to display current image
        extended_timeout = max(25, int(dwell_time * 2))

    while time_waited < extended_timeout:
        # Create a task to wait on the sync manager waiter (for cross-thread/worker notifications)
        waiter_task = asyncio.ensure_future(
            loop.run_in_executor(None, waiter.wait, poll_interval)
        )

        # Create a task to wait on the displaying event (for device acknowledgments)
        display_task = asyncio.create_task(ack.displaying_event.wait())

        # Wait for either the waiter notification, display ack, or timeout
        done, pending = await asyncio.wait(
            {waiter_task, display_task},
            timeout=poll_interval,
            return_when=asyncio.FIRST_COMPLETED,
        )

        # Cancel any pending tasks
        for task in pending:
            task.cancel()
            try:
                await task
            except asyncio.CancelledError:
                pass

        # Check what triggered the wakeup
        if display_task in done:
            # Got displaying acknowledgment, render next image
            logger.debug(f"[{device.id}] Device acknowledged display")
            response = await loop.run_in_executor(
                None, next_app_logic, db_conn, user, device
            )
            return (response, last_brightness)

        sync_payload = None
        if waiter_task in done:
            try:
                sync_payload = waiter_task.result()
            except Exception as e:
                logger.warning(
                    f"[{device.id}] Unexpected error getting waiter result: {e}"
                )

        # If woken by a push notification, process the payload
        if sync_payload:
            if isinstance(sync_payload.payload, bytes):
                logger.debug(
                    f"[{device.id}] Image payload received, sending immediately"
                )
                # Create a response directly from the image bytes
                response = Response(
                    content=sync_payload.payload, media_type="image/webp"
                )
                # Set immediate flag so device displays it right away
                response.headers["Tronbyt-Immediate"] = "1"
                # Set a default dwell time, as it's an immediate push
                response.headers["Tronbyt-Dwell-Secs"] = "5"
                return (response, last_brightness)
            elif isinstance(sync_payload.payload, int):
                logger.debug(
                    f"[{device.id}] Brightness payload received, sending immediately"
                )
                try:
                    await _send_message(
                        websocket,
                        BrightnessMessage(brightness=sync_payload.payload),
                    )
                    last_brightness = sync_payload.payload
                except Exception as e:
                    logger.error(f"[{device.id}] Failed to send brightness: {e}")

        # Update time waited
        time_waited += poll_interval

    # Timeout reached without acknowledgment
    if not ack.displaying_event.is_set() and device.info.protocol_version is not None:
        logger.debug(
            f"[{device.id}] No display confirmation received after {extended_timeout}s"
        )

    # Render next image after timeout
    response = await loop.run_in_executor(None, next_app_logic, db_conn, user, device)
    return (response, last_brightness)


async def sender(
    websocket: WebSocket,
    device_id: str,
    db_conn: sqlite3.Connection,
    ack: DeviceAcknowledgment,
) -> None:
    """The sender task for the websocket."""
    user = db.get_user_by_device_id(db_conn, device_id)
    if not user:
        logger.error(f"[{device_id}] User not found, sender task cannot start.")
        return
    device = user.devices.get(device_id)
    if not device:
        logger.error(f"[{device_id}] Device not found, sender task cannot start.")
        return

    waiter = get_sync_manager().get_waiter(device_id)
    loop = asyncio.get_running_loop()
    last_brightness = -1
    dwell_time = 5

    try:
        # Render the first image before entering the loop
        response = await loop.run_in_executor(
            None, next_app_logic, db_conn, user, device
        )

        # Main loop
        while True:
            # Reset acknowledgment events for next image
            ack.reset()

            if response is not None:
                # Send the previously rendered image
                dwell_time, last_brightness = await _send_response(
                    websocket, response, last_brightness
                )

            # Refresh user and device from DB in case of updates
            user = db.get_user_by_device_id(db_conn, device_id)
            if not user:
                logger.error(f"[{device_id}] user gone, stopping websocket sender.")
                return
            device = user.devices.get(device_id)
            if not device:
                logger.error(f"[{device_id}] device gone, stopping websocket sender.")
                return

            # Wait for device acknowledgment with timeout and ephemeral push detection
            # This will check for ephemeral pushes and render the next image when ready
            response, last_brightness = await _wait_for_acknowledgment(
                ack,
                dwell_time,
                db_conn,
                loop,
                waiter,
                websocket,
                last_brightness,
                user,
                device,
            )

    except asyncio.CancelledError:
        pass  # Expected on disconnect
    except Exception as e:
        logger.error(f"WebSocket sender error for device {device_id}: {e}")
    finally:
        waiter.close()


async def receiver(
    websocket: WebSocket, device_id: str, db_conn: sqlite3.Connection
) -> None:
    """The receiver task for the websocket."""
    adapter: TypeAdapter[ClientMessage] = TypeAdapter(ClientMessage)
    try:
        while True:
            message = await websocket.receive_text()
            try:
                parsed_message = adapter.validate_json(message)

                user = db.get_user_by_device_id(db_conn, device_id)
                if not user:
                    logger.warning(
                        f"[{device_id}] User not found for device, cannot process message."
                    )
                    continue

                try:
                    with db.db_transaction(db_conn) as cursor:
                        db.update_device_field(
                            cursor,
                            user.username,
                            device_id,
                            "last_seen",
                            datetime.now(timezone.utc).isoformat(),
                        )

                        # Fetch the ACK object for the CURRENTLY active connection
                        ack = await connection_manager.get_ack(device_id)
                        if not ack:
                            logger.warning(
                                f"[{device_id}] Received message but no active connection found, ignoring."
                            )
                            # Still save the last_seen update
                            return  # Exit the 'with' block, committing changes

                        if isinstance(parsed_message, QueuedMessage):
                            logger.debug(
                                f"[{device_id}] Image queued (seq: {parsed_message.queued})"
                            )
                            ack.mark_queued(parsed_message.queued)

                            # If we get a queued message, it's a new firmware device.
                            # Update protocol version if not set.
                            device = user.devices.get(device_id)
                            if device and device.info.protocol_version is None:
                                logger.info(
                                    f"[{device_id}] First 'queued' message, setting protocol_version to 1"
                                )
                                db.update_device_field(
                                    cursor,
                                    user.username,
                                    device_id,
                                    "info.protocol_version",
                                    1,
                                )
                        elif isinstance(parsed_message, DisplayingMessage):
                            logger.debug(
                                f"[{device_id}] Image displaying (seq: {parsed_message.displaying})"
                            )
                            ack.mark_displaying(parsed_message.displaying)
                        elif isinstance(parsed_message, DisplayingStatusMessage):
                            logger.debug(
                                f"[{device_id}] Image displaying (seq: {parsed_message.counter})"
                            )
                            ack.mark_displaying(parsed_message.counter)
                        elif isinstance(parsed_message, ClientInfoMessage):
                            logger.debug(
                                f"[{device_id}] Received ClientInfoMessage: {parsed_message.client_info.model_dump_json()}"
                            )
                            client_info = parsed_message.client_info
                            info_updates = {
                                "firmware_version": client_info.firmware_version,
                                "firmware_type": client_info.firmware_type,
                                "protocol_version": client_info.protocol_version,
                                "mac_address": client_info.mac_address,
                            }
                            for field, value in info_updates.items():
                                if value is not None:
                                    db.update_device_field(
                                        cursor,
                                        user.username,
                                        device_id,
                                        f"info.{field}",
                                        value,
                                    )
                            logger.info(
                                f"[{device_id}] Updated device info via websocket"
                            )
                        else:
                            # This should not happen if the models cover all cases
                            logger.warning(
                                f"[{device_id}] Unhandled message format: {message}"
                            )
                except sqlite3.Error as e:
                    logger.error(f"Database error in websocket receiver: {e}")

            except (ValueError, ValidationError) as e:
                logger.warning(f"[{device_id}] Failed to parse device message: {e}")

    except WebSocketDisconnect:
        logger.info(f"WebSocket disconnected for device {device_id}")


@router.websocket("/{device_id}/ws")
async def websocket_endpoint(
    websocket: WebSocket,
    device_id: str,
    db_conn: sqlite3.Connection = Depends(get_db),
) -> None:
    """WebSocket endpoint for devices."""
    if not re.match(r"^[a-fA-F0-9]{8}$", device_id):
        await websocket.close(code=status.WS_1008_POLICY_VIOLATION)
        return

    user = db.get_user_by_device_id(db_conn, device_id)
    if not user:
        await websocket.close(code=status.WS_1008_POLICY_VIOLATION)
        return

    device = user.devices.get(device_id)
    if not device:
        await websocket.close(code=status.WS_1008_POLICY_VIOLATION)
        return

    await websocket.accept()

    # Immediately update protocol type to WS on successful connection
    try:
        with db.db_transaction(db_conn) as cursor:
            db.update_device_field(
                cursor,
                user.username,
                device_id,
                "info.protocol_type",
                ProtocolType.WS.value,
            )
        logger.info(f"[{device_id}] Updated protocol_type to WS on connect")
    except sqlite3.Error as e:
        logger.error(f"[{device_id}] Failed to update protocol_type on connect: {e}")

    # Create shared acknowledgment state for sender/receiver communication
    ack = DeviceAcknowledgment()

    # Create tasks for the new connection
    sender_task = asyncio.create_task(
        sender(websocket, device_id, db_conn, ack), name=f"ws_sender_{device_id}"
    )
    receiver_task = asyncio.create_task(
        receiver(websocket, device_id, db_conn), name=f"ws_receiver_{device_id}"
    )

    # Register the new connection, which handles cleanup of any old one
    await connection_manager.register(
        device_id, websocket, ack, sender_task, receiver_task
    )

    try:
        done, pending = await asyncio.wait(
            [sender_task, receiver_task], return_when=asyncio.FIRST_COMPLETED
        )

        for task in done:
            try:
                task.result()
            except WebSocketDisconnect:
                logger.info(f"WebSocket disconnected for device {device_id}")
                break
            except Exception as e:
                logger.error(f"WebSocket task failed for device {device_id}: {e}")
                break

        for task in pending:
            task.cancel()

        await asyncio.gather(*pending, return_exceptions=True)
    finally:
        # Unregister the connection and signal cleanup completion
        await connection_manager.unregister(device_id, websocket)
