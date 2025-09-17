from fastapi import WebSocket


class ConnectionManager:
    def __init__(self):
        self.active_connections: dict[str, WebSocket] = {}

    async def connect(self, websocket: WebSocket, device_id: str):
        await websocket.accept()
        self.active_connections[device_id] = websocket

    def disconnect(self, device_id: str):
        if device_id in self.active_connections:
            del self.active_connections[device_id]

    async def send_personal_message(self, message: str, device_id: str):
        websocket = self.active_connections.get(device_id)
        if websocket:
            await websocket.send_text(message)

    async def send_binary_message(self, message: bytes, device_id: str):
        websocket = self.active_connections.get(device_id)
        if websocket:
            await websocket.send_bytes(message)


manager = ConnectionManager()
