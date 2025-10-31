"""Synchronization primitives for Tronbyt Server."""

import logging
from abc import ABC, abstractmethod
from multiprocessing import Manager
from typing import Any, cast

import redis
from threading import Lock

from tronbyt_server.config import get_settings

# Type alias for multiprocessing.Condition, which is not a class
ConditionType = Any

NOTIFY_MESSAGE = "notify"

logger = logging.getLogger(__name__)


class Waiter(ABC):
    """Abstract base class for a waiter."""

    @abstractmethod
    def wait(self, timeout: int) -> None:
        """Wait for a notification."""
        raise NotImplementedError

    @abstractmethod
    def close(self) -> None:
        """Clean up the waiter."""
        raise NotImplementedError


class SyncManager(ABC):
    """Abstract base class for synchronization managers."""

    @abstractmethod
    def get_waiter(self, device_id: str) -> Waiter:
        """Get a waiter for a given device ID."""
        raise NotImplementedError

    @abstractmethod
    def notify(self, device_id: str) -> None:
        """Notify waiters for a given device ID."""
        raise NotImplementedError

    @abstractmethod
    def shutdown(self) -> None:
        """Shut down the sync manager."""
        raise NotImplementedError


class MultiprocessingWaiter(Waiter):
    """A waiter that uses multiprocessing primitives."""

    def __init__(
        self,
        condition: "ConditionType",
        manager: "MultiprocessingSyncManager",
        device_id: str,
    ):
        self._condition = condition
        self._manager = manager
        self._device_id = device_id

    def wait(self, timeout: int) -> None:
        """Wait for a notification."""
        with self._condition:
            if not self._manager._shutdown_event.is_set():
                self._condition.wait(timeout=timeout)

    def close(self) -> None:
        """Clean up the waiter."""
        self._manager._release_condition(self._device_id)


class MultiprocessingSyncManager(SyncManager):
    """A synchronization manager that uses multiprocessing primitives."""

    def __init__(self) -> None:
        manager = Manager()
        self._conditions: dict[str, "ConditionType"] = cast(
            dict[str, "ConditionType"], manager.dict()
        )
        self._waiter_counts: dict[str, int] = cast(dict[str, int], manager.dict())
        self._lock = manager.Lock()
        self._manager = manager
        self._shutdown_event = manager.Event()

    def _release_condition(self, device_id: str) -> None:
        """Decrement waiter count and clean up condition if no waiters are left."""
        with self._lock:
            if device_id in self._waiter_counts:
                self._waiter_counts[device_id] -= 1
                if self._waiter_counts[device_id] == 0:
                    del self._conditions[device_id]
                    del self._waiter_counts[device_id]

    def get_waiter(self, device_id: str) -> Waiter:
        """Get a waiter for a given device ID."""
        with self._lock:
            if device_id not in self._conditions:
                self._conditions[device_id] = self._manager.Condition()
                self._waiter_counts[device_id] = 0
            self._waiter_counts[device_id] += 1
            condition = self._conditions[device_id]
        return MultiprocessingWaiter(condition, self, device_id)

    def notify(self, device_id: str) -> None:
        """Notify waiters for a given device ID."""
        condition: "ConditionType" | None = None
        with self._lock:
            if device_id in self._conditions:
                condition = self._conditions[device_id]

        if condition:
            with condition:
                condition.notify_all()

    def shutdown(self) -> None:
        """Shut down the sync manager."""
        self._shutdown_event.set()
        with self._lock:
            for condition in self._conditions.values():
                with condition:
                    condition.notify_all()


class RedisWaiter(Waiter):
    """A waiter that uses Redis Pub/Sub."""

    def __init__(self, redis_client: redis.Redis, device_id: str):
        # ignore_subscribe_messages=True prevents subscription confirmation messages
        # from being delivered to the consumer. This is useful for simple notification
        # use-cases, but may hide connection/debugging issues. Consider making this
        # configurable if you need to debug subscription events.
        self._pubsub = redis_client.pubsub(  # type: ignore[no-untyped-call]
            ignore_subscribe_messages=True
        )
        self._device_id = device_id
        self._pubsub.subscribe(self._device_id)

    def wait(self, timeout: int) -> None:
        """Wait for a notification."""
        try:
            self._pubsub.get_message(timeout=timeout)
        except ValueError:
            # This can happen if the pubsub connection is closed by another thread
            # while we are waiting for a message.
            pass

    def close(self) -> None:
        """Clean up the waiter."""
        try:
            self._pubsub.unsubscribe(self._device_id)
        except ConnectionError:
            # Ignore connection errors during cleanup
            pass
        finally:
            self._pubsub.close()


class RedisSyncManager(SyncManager):
    """A synchronization manager that uses Redis Pub/Sub."""

    def __init__(self, redis_url: str) -> None:
        self._redis = redis.from_url(redis_url)  # type: ignore[no-untyped-call]

    def get_waiter(self, device_id: str) -> Waiter:
        """Get a waiter for a given device ID."""
        return RedisWaiter(self._redis, device_id)

    def notify(self, device_id: str) -> None:
        """Notify waiters for a given device ID."""
        self._redis.publish(device_id, NOTIFY_MESSAGE)

    def shutdown(self) -> None:
        """Shut down the sync manager."""
        self._redis.close()


_sync_manager: SyncManager | None = None
_sync_manager_lock = Lock()


def get_sync_manager() -> SyncManager:
    """Get the synchronization manager for the application."""
    global _sync_manager
    if _sync_manager is None:
        with _sync_manager_lock:
            if _sync_manager is None:  # Double-checked locking
                settings = get_settings()
                redis_url = settings.REDIS_URL
                if redis_url:
                    logger.info("Using Redis for synchronization")
                    _sync_manager = RedisSyncManager(redis_url)
                else:
                    logger.info("Using multiprocessing for synchronization")
                    _sync_manager = MultiprocessingSyncManager()
    assert _sync_manager is not None
    return _sync_manager
