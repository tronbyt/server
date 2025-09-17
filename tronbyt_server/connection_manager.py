import asyncio
from fastapi import WebSocket
import redis.asyncio as redis
from typing import Dict, List
from tronbyt_server.config import get_settings

class ConnectionManager:
    def __init__(self):
        self.settings = get_settings()
        self.redis_pool = redis.ConnectionPool.from_url(self.settings.redis_url, decode_responses=True)
        self.active_connections: Dict[str, WebSocket] = {}
        self.queues: Dict[str, asyncio.Queue] = {}
        self.pubsub_client = None
        self.listener_task = None

    async def connect(self, websocket: WebSocket, client_id: str):
        await websocket.accept()
        self.active_connections[client_id] = websocket
        self.queues[client_id] = asyncio.Queue()
        if self.listener_task is None:
            self.listener_task = asyncio.create_task(self._listener())

    def disconnect(self, client_id: str):
        if client_id in self.active_connections:
            del self.active_connections[client_id]
        if client_id in self.queues:
            del self.queues[client_id]

    async def broadcast(self, message: str):
        redis_client = redis.Redis(connection_pool=self.redis_pool)
        await redis_client.publish("websockets", message)

    async def get_message(self, client_id: str):
        if client_id in self.queues:
            return await self.queues[client_id].get()

    async def _listener(self):
        redis_client = redis.Redis(connection_pool=self.redis_pool)
        self.pubsub_client = redis_client.pubsub()
        await self.pubsub_client.subscribe("websockets")
        while True:
            try:
                message = await self.pubsub_client.get_message(ignore_subscribe_messages=True, timeout=1.0)
                if message:
                    # In a real application, the message should contain the client_id
                    # so we can send to a specific client. For now, we broadcast to all.
                    for queue in self.queues.values():
                        await queue.put(message['data'])
            except Exception as e:
                print(f"Error in listener: {e}")
                # Reconnect logic might be needed here
                await asyncio.sleep(5)


manager = ConnectionManager()
