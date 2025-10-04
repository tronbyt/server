"""Websockets router."""

import asyncio
import json
import logging
import sqlite3
from typing import cast

from fastapi import APIRouter, WebSocket, status

import re
from tronbyt_server import db
from tronbyt_server.routers.manager import _next_app_logic
from tronbyt_server.sync import get_sync_manager

router = APIRouter(tags=["websockets"])
logger = logging.getLogger(__name__)


@router.websocket("/{device_id}/ws")
async def websocket_endpoint(websocket: WebSocket, device_id: str) -> None:
    """WebSocket endpoint for devices."""
    if not re.match(r"^[a-fA-F0-9]{8}$", device_id):
        await websocket.close(code=status.WS_1008_POLICY_VIOLATION)
        return

    db_conn: sqlite3.Connection = db.get_db()
    user = db.get_user_by_device_id(db_conn, device_id)
    if not user:
        await websocket.close(code=status.WS_1008_POLICY_VIOLATION)
        return

    device = user.devices.get(device_id)
    if not device:
        await websocket.close(code=status.WS_1008_POLICY_VIOLATION)
        return

    await websocket.accept()
    waiter = get_sync_manager(logger).get_waiter(device_id)

    async def reader() -> None:
        while True:
            try:
                waiter.wait(timeout=60)
                await send_next_image()
            except Exception as e:
                logger.error(f"Error in reader: {e}")
                break

    async def send_next_image() -> int:
        try:
            response = _next_app_logic(db_conn, device_id)
            if response.status_code == 200:
                brightness = response.headers.get("Tronbyt-Brightness")
                if brightness is not None:
                    await websocket.send_text(
                        json.dumps({"brightness": int(brightness)})
                    )
                await websocket.send_bytes(cast(bytes, response.body))
                return int(response.headers.get("Tronbyt-Dwell-Secs", 5))
            else:
                await websocket.send_text(
                    json.dumps(
                        {
                            "status": "error",
                            "message": f"Error fetching image: {response.status_code}",
                        }
                    )
                )
        except Exception as e:
            logger.error(f"Error in send_next_image: {e}")
        return 5

    reader_task = asyncio.create_task(reader())
    try:
        while True:
            dwell_time = await send_next_image()
            await asyncio.sleep(dwell_time)
    except Exception as e:
        logger.error(f"WebSocket error: {e}")
    finally:
        reader_task.cancel()
        waiter.close()
        await websocket.close()
        db_conn.close()
