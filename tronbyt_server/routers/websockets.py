"""Websockets router."""

import asyncio
import json
import logging
import sqlite3
from dataclasses import dataclass
from pathlib import Path
from typing import Any, cast

from fastapi import APIRouter, Depends, WebSocket, status, Response
from fastapi.responses import FileResponse
from starlette.websockets import WebSocketDisconnect

import re
from tronbyt_server import db
from tronbyt_server.dependencies import get_db
from tronbyt_server.routers.manager import _next_app_logic
from tronbyt_server.sync import get_sync_manager

router = APIRouter(tags=["websockets"])
logger = logging.getLogger(__name__)


class DeviceAcknowledgment:
    """Manages device acknowledgment state for queued/displaying messages."""

    def __init__(self, device_id: str) -> None:
        """Initializes the DeviceAcknowledgment with a device_id."""
        self.device_id = device_id
        self.queued_event = asyncio.Event()
        self.displaying_event = asyncio.Event()
        self.old_firmware_detected = False
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
        # If we receive queued message, device is new firmware
        if self.old_firmware_detected:
            logger.debug(
                f"[{self.device_id}] Received 'queued' message - device is new firmware, resetting detection"
            )
            self.old_firmware_detected = False

    def mark_displaying(self, counter: int) -> None:
        """Mark image as displaying by device."""
        self.displaying_counter = counter
        self.displaying_event.set()

    def mark_old_firmware(self) -> None:
        """Mark device as old firmware."""
        if not self.old_firmware_detected:
            logger.info(
                f"[{self.device_id}] No 'displaying' message after timeout - marking as old firmware"
            )
            self.old_firmware_detected = True


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

        old_connection: "Connection" | None
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

    async def get_websocket(self, device_id: str) -> WebSocket | None:
        """Get the websocket for a given device_id atomically."""
        async with self._lock:
            connection = self._active_connections.get(device_id)
            if connection:
                return connection.websocket
            return None


# Global instance of the connection manager
connection_manager = ConnectionManager()


async def send_brightness_update(device_id: str, brightness: int) -> bool:
    """Send a brightness update to an active websocket connection.

    Returns True if sent successfully, False if no active connection.
    """
    websocket = await connection_manager.get_websocket(device_id)
    if not websocket:
        return False

    try:
        await websocket.send_text(json.dumps({"brightness": brightness}))
        logger.info(f"[{device_id}] Sent brightness update: {brightness}")
        return True
    except Exception as e:
        logger.error(f"[{device_id}] Failed to send brightness update: {e}")
        return False


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
        await websocket.send_text(
            json.dumps(
                {
                    "dwell_secs": dwell_time,
                }
            )
        )

        # Update confirmation timeout now that we have the actual dwell time
        # confirmation_timeout = dwell_time

        # Send brightness as a text message, if it has changed
        # This must be done before sending the image so that the new value is applied to the next image
        brightness_str = response.headers.get("Tronbyt-Brightness")
        if brightness_str:
            brightness = int(brightness_str)
            if brightness != last_brightness:
                await websocket.send_text(json.dumps({"brightness": brightness}))
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
            await websocket.send_text(
                json.dumps(
                    {
                        "immediate": True,
                    }
                )
            )

        dwell_time = int(response.headers.get("Tronbyt-Dwell-Secs", 5))
    else:
        await websocket.send_text(
            json.dumps(
                {
                    "status": "error",
                    "message": f"Error fetching image: {response.status_code}",
                }
            )
        )
    return dwell_time, last_brightness


async def _wait_for_acknowledgment(
    device_id: str,
    ack: DeviceAcknowledgment,
    dwell_time: int,
    db_conn: sqlite3.Connection,
    loop: asyncio.AbstractEventLoop,
    waiter: Any,  # Waiter (using Any to avoid mypy issues with abstract base class)
    websocket: WebSocket,
    last_brightness: int,
) -> tuple[Response, int]:
    """Wait for device to acknowledge displaying the image, with timeout and ephemeral push detection.

    Returns tuple of (next Response to send, updated last_brightness).
    """
    poll_interval = 1  # Check every second
    time_waited = 0

    # Determine timeout based on firmware type
    if ack.old_firmware_detected:
        # Old firmware doesn't send messages, just wait for dwell_time
        extended_timeout = dwell_time
        logger.debug(
            f"[{device_id}] Using old firmware timeout of {extended_timeout}s (dwell_time)"
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
            logger.debug(f"[{device_id}] Device acknowledged display")
            response = await loop.run_in_executor(
                None, _next_app_logic, db_conn, device_id
            )
            return (response, last_brightness)

        # Check if there's an ephemeral push waiting (this is common when woken by waiter)
        pushed_dir = db.get_device_webp_dir(device_id) / "pushed"
        ephemeral_exists = await loop.run_in_executor(
            None, lambda: pushed_dir.is_dir() and any(pushed_dir.glob("__*"))
        )

        if ephemeral_exists:
            logger.debug(
                f"[{device_id}] Ephemeral push detected, interrupting wait to send immediately"
            )
            # Render the next app (which will pick up the ephemeral push)
            try:
                response = await loop.run_in_executor(
                    None, _next_app_logic, db_conn, device_id
                )
                logger.debug(
                    f"[{device_id}] Ephemeral push rendered, will send immediately"
                )
                return (response, last_brightness)
            except Exception as e:
                logger.error(f"[{device_id}] Error rendering ephemeral push: {e}")
                # Continue waiting if render failed

        # If woken by waiter but no ephemeral files, check for brightness changes
        # and send immediately if changed, then render next image
        if waiter_task in done and time_waited > 0:
            logger.debug(
                f"[{device_id}] Woken by push notification, checking for updates"
            )

            # Check if brightness has changed and send update immediately
            user = await loop.run_in_executor(
                None, db.get_user_by_device_id, db_conn, device_id
            )
            if user:
                device = user.devices.get(device_id)
                if device:
                    new_brightness = db.get_device_brightness_percent(device)
                    if new_brightness != last_brightness:
                        logger.info(
                            f"[{device_id}] Brightness changed to {new_brightness}, sending immediately"
                        )
                        try:
                            await websocket.send_text(
                                json.dumps({"brightness": new_brightness})
                            )
                            last_brightness = (
                                new_brightness  # Update tracked brightness
                            )
                            ack.brightness_to_send = None  # Clear pending brightness
                        except Exception as e:
                            logger.error(
                                f"[{device_id}] Failed to send brightness: {e}"
                            )

            # Then render and send next image
            response = await loop.run_in_executor(
                None, _next_app_logic, db_conn, device_id
            )
            return (response, last_brightness)

        # Update time waited
        time_waited += poll_interval

    # Timeout reached without acknowledgment
    if not ack.displaying_event.is_set():
        ack.mark_old_firmware()
        logger.debug(
            f"[{device_id}] No display confirmation received after {extended_timeout}s, assuming old firmware"
        )

    # Render next image after timeout
    response = await loop.run_in_executor(None, _next_app_logic, db_conn, device_id)
    return (response, last_brightness)


async def sender(
    websocket: WebSocket,
    device_id: str,
    db_conn: sqlite3.Connection,
    ack: DeviceAcknowledgment,
) -> None:
    """The sender task for the websocket."""
    waiter = get_sync_manager(logger).get_waiter(device_id)
    loop = asyncio.get_running_loop()
    last_brightness = -1
    dwell_time = 5

    try:
        # Render the first image before entering the loop
        response = await loop.run_in_executor(None, _next_app_logic, db_conn, device_id)

        # Main loop
        while True:
            # Reset acknowledgment events for next image
            ack.reset()

            # Send the previously rendered image
            dwell_time, last_brightness = await _send_response(
                websocket, response, last_brightness
            )

            # Wait for device acknowledgment with timeout and ephemeral push detection
            # This will check for ephemeral pushes and render the next image when ready
            response, last_brightness = await _wait_for_acknowledgment(
                device_id,
                ack,
                dwell_time,
                db_conn,
                loop,
                waiter,
                websocket,
                last_brightness,
            )

    except asyncio.CancelledError:
        pass  # Expected on disconnect
    except Exception as e:
        logger.error(f"WebSocket sender error for device {device_id}: {e}")
    finally:
        waiter.close()


async def receiver(
    websocket: WebSocket, device_id: str, ack: DeviceAcknowledgment
) -> None:
    """The receiver task for the websocket."""
    try:
        while True:
            message = await websocket.receive_text()
            try:
                msg_data = json.loads(message)

                if "queued" in msg_data:
                    # Device has queued/buffered the image: {"queued": counter}
                    queued_counter = msg_data.get("queued")
                    logger.debug(f"[{device_id}] Image queued (seq: {queued_counter})")
                    ack.mark_queued(queued_counter)

                elif "displaying" in msg_data or msg_data.get("status") == "displaying":
                    # Device has started displaying: {"displaying": counter} or {"status": "displaying", "counter": X}
                    display_seq = msg_data.get("displaying") or msg_data.get("counter")
                    logger.debug(f"[{device_id}] Image displaying (seq: {display_seq})")
                    ack.mark_displaying(display_seq)

                else:
                    # Unknown message format
                    logger.warning(f"[{device_id}] Unknown message format: {message}")

            except (json.JSONDecodeError, ValueError) as e:
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

    # Create shared acknowledgment state for sender/receiver communication
    ack = DeviceAcknowledgment(device_id)

    # Create tasks for the new connection
    sender_task = asyncio.create_task(
        sender(websocket, device_id, db_conn, ack), name=f"ws_sender_{device_id}"
    )
    receiver_task = asyncio.create_task(
        receiver(websocket, device_id, ack), name=f"ws_receiver_{device_id}"
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
