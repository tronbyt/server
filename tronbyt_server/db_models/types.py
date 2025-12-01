"""Custom SQLAlchemy types for Tronbyt database models."""

from typing import Any
from sqlalchemy import TypeDecorator, Integer

from tronbyt_server.models.device import Brightness as BrightnessModel


class Brightness(TypeDecorator):
    """SQLAlchemy type for Brightness objects.

    Stores as integer (0-100) in database, converts to/from Brightness objects in Python.
    """

    impl = Integer
    cache_ok = True

    def process_bind_param(
        self, value: BrightnessModel | int | None, dialect: Any
    ) -> int | None:
        """Convert Brightness object to int for storage."""
        if value is None:
            return None
        if isinstance(value, BrightnessModel):
            return value.as_percent
        return value

    def process_result_value(
        self, value: int | None, dialect: Any
    ) -> BrightnessModel | None:
        """Convert int from database to Brightness object."""
        if value is None:
            return None
        return BrightnessModel(value)
