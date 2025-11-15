from pydantic import BaseModel, field_validator, field_serializer
import base64
import binascii
from typing import Type, Any


class SyncPayload(BaseModel):
    payload: bytes | int

    @field_serializer("payload")
    def serialize_payload(self, payload: bytes | int, _info: Any) -> str | int:
        if isinstance(payload, bytes):
            return base64.b64encode(payload).decode("ascii")
        return payload

    @field_validator("payload", mode="before")
    @classmethod
    def decode_base64(cls: Type["SyncPayload"], v: Any) -> bytes | int:
        if isinstance(v, str):
            try:
                return base64.b64decode(v)
            except binascii.Error:
                # If it's a string but not base64, let Pydantic handle it as a string
                # which will then fail if the target type is bytes or int
                pass
        if isinstance(v, (bytes, int)):
            return v
        raise ValueError("Payload must be bytes, int, or a base64-encoded string")
