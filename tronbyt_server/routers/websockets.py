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


async def _send_response(
    websocket: WebSocket, response: Response, last_brightness: int
) -> tuple[int, int]:
    """Send a response to the websocket."""
    dwell_time = 5
    if response.status_code == 200:
        brightness_str = response.headers.get("Tronbyt-Brightness")
        if brightness_str:
            brightness = int(brightness_str)
            if brightness != last_brightness:
                await websocket.send_text(json.dumps({"brightness": brightness}))
                last_brightness = brightness

        if isinstance(response, FileResponse):
            content = await asyncio.get_running_loop().run_in_executor(
                None, Path(response.path).read_bytes
            )
            await websocket.send_bytes(content)
        else:
            await websocket.send_bytes(cast(bytes, response.body))
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


async def sender(
    websocket: WebSocket, device_id: str, db_conn: sqlite3.Connection
) -> None:
    """The sender task for the websocket."""
    waiter = get_sync_manager(logger).get_waiter(device_id)
    loop = asyncio.get_running_loop()
    last_brightness = -1
    dwell_time = 5

    try:
        # Main loop
        while True:
            response = await loop.run_in_executor(
                None, _next_app_logic, db_conn, device_id
            )
            dwell_time, last_brightness = await _send_response(
                websocket, response, last_brightness
            )
            await loop.run_in_executor(None, waiter.wait, dwell_time)

    except asyncio.CancelledError:
        pass  # Expected on disconnect
    except Exception as e:
        logger.error(f"WebSocket sender error for device {device_id}: {e}")
    finally:
        waiter.close()


async def receiver(websocket: WebSocket, device_id: str) -> None:
    """The receiver task for the websocket."""
    try:
        while True:
            await websocket.receive_text()
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

    sender_task = asyncio.create_task(sender(websocket, device_id, db_conn))
    receiver_task = asyncio.create_task(receiver(websocket, device_id))

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
