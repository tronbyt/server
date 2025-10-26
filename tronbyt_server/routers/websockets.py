"""Websockets router."""

import asyncio
import json
import logging
import sqlite3
from pathlib import Path
from typing import cast

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

    def __init__(self) -> None:
        self.queued_event = asyncio.Event()
        self.displaying_event = asyncio.Event()
        self.old_firmware_detected = False
        self.queued_counter: int | None = None
        self.displaying_counter: int | None = None

    def reset(self) -> None:
        """Reset events for next image."""
        self.queued_event.clear()
        self.displaying_event.clear()
        self.queued_counter = None
        self.displaying_counter = None

    def mark_queued(self, counter: int) -> None:
        """Mark image as queued by device."""
        self.queued_counter = counter
        self.queued_event.set()
        # If we receive queued message, device is new firmware
        if self.old_firmware_detected:
            logger.debug(
                "Received 'queued' message - device is new firmware, resetting detection"
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
                "No 'displaying' message after timeout - marking as old firmware"
            )
            self.old_firmware_detected = True


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
) -> Response:
    """Wait for device to acknowledge displaying the image, with timeout and ephemeral push detection.

    Returns the next Response to send.
    """
    poll_interval = 1  # Check every second
    time_waited = 0

    # Determine timeout based on firmware type
    if ack.old_firmware_detected:
        # Old firmware doesn't send messages, just wait for dwell_time
        extended_timeout = dwell_time
        logger.debug(f"Using old firmware timeout of {extended_timeout}s (dwell_time)")
    else:
        # New firmware - give device full dwell time + buffer
        # Use 2x dwell time to give plenty of room for the device to display current image
        extended_timeout = max(25, int(dwell_time * 2))

    while time_waited < extended_timeout:
        # First check if there's an ephemeral push waiting
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
                return response
            except Exception as e:
                logger.error(f"Error rendering ephemeral push: {e}")
                # Continue waiting if render failed

        # Wait for displaying event with timeout
        try:
            await asyncio.wait_for(ack.displaying_event.wait(), timeout=poll_interval)
            # Got displaying acknowledgment, render next image
            logger.debug(
                f"Image displaying acknowledged (seq: {ack.displaying_counter})"
            )
            return await loop.run_in_executor(None, _next_app_logic, db_conn, device_id)
        except asyncio.TimeoutError:
            # No acknowledgment yet, continue waiting
            time_waited += poll_interval

    # Timeout reached without acknowledgment
    if not ack.displaying_event.is_set():
        ack.mark_old_firmware()
        logger.debug(
            f"No display confirmation received after {extended_timeout}s, assuming old firmware"
        )

    # Render next image after timeout
    return await loop.run_in_executor(None, _next_app_logic, db_conn, device_id)


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
            response = await _wait_for_acknowledgment(
                device_id, ack, dwell_time, db_conn, loop
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
                    logger.debug(f"Image queued (seq: {queued_counter})")
                    ack.mark_queued(queued_counter)

                elif "displaying" in msg_data or msg_data.get("status") == "displaying":
                    # Device has started displaying: {"displaying": counter} or {"status": "displaying", "counter": X}
                    display_seq = msg_data.get("displaying") or msg_data.get("counter")
                    logger.debug(f"Image displaying (seq: {display_seq})")
                    ack.mark_displaying(display_seq)

                else:
                    # Unknown message format
                    logger.warning(f"[{device_id}] Unknown message format: {message}")

            except (json.JSONDecodeError, ValueError) as e:
                logger.warning(f"Failed to parse device message: {e}")

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
    ack = DeviceAcknowledgment()

    sender_task = asyncio.create_task(sender(websocket, device_id, db_conn, ack))
    receiver_task = asyncio.create_task(receiver(websocket, device_id, ack))

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
