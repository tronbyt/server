"""Synchronization primitives for Tronbyt Server."""

import logging
import os
import ast
import base64
import redis
from abc import ABC, abstractmethod
from multiprocessing import current_process
from multiprocessing.synchronize import Event as MPEvent
from multiprocessing.synchronize import Lock as MPLock
from multiprocessing.managers import (
    SyncManager,
    DictProxy,
)
from typing import Any, cast
from threading import Lock

from tronbyt_server.config import get_settings

# Type alias for multiprocessing.Condition, which is not a class
ConditionType = Any

NOTIFY_MESSAGE = "notify"

logger = logging.getLogger(__name__)


class Waiter(ABC):
    """Abstract base class for a waiter."""

    @abstractmethod
    def wait(self, timeout: int) -> bool:
        """Wait for a notification."""
        raise NotImplementedError

    @abstractmethod
    def close(self) -> None:
        """Clean up the waiter."""
        raise NotImplementedError


class AbstractSyncManager(ABC):
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
    def _shutdown(self) -> None:
        """Shut down the sync manager."""
        raise NotImplementedError

    @abstractmethod
    def __enter__(self) -> "AbstractSyncManager":
        """Enter the context manager."""
        raise NotImplementedError

    @abstractmethod
    def __exit__(self, exc_type: Any, exc_value: Any, traceback: Any) -> None:
        """Exit the context manager."""
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

    def wait(self, timeout: int) -> bool:
        """Wait for a notification."""
        with self._condition:
            if self._manager.is_shutdown():
                return False
            notified = self._condition.wait(timeout=timeout)
            if self._manager.is_shutdown():
                return False
            return bool(notified)

    def close(self) -> None:
        """Clean up the waiter."""
        self._manager.release_condition(self._device_id)


class ServerSyncManager(SyncManager):
    """A custom SyncManager that creates and vends singleton sync primitives."""

    _init_lock = Lock()
    _initialized = False

    _conditions: DictProxy[Any, Any]
    _waiter_counts: DictProxy[Any, Any]
    _lock: MPLock
    _shutdown_event: MPEvent

    def _lazy_init(self) -> None:
        """
        Initialize the shared primitives using double-checked locking to avoid
        acquiring the lock on every call.
        """
        if not self._initialized:
            with self._init_lock:
                if not self._initialized:
                    self._conditions = self.dict()
                    self._waiter_counts = self.dict()
                    self._lock = cast(MPLock, self.Lock())
                    self._shutdown_event = cast(MPEvent, self.Event())
                    self._initialized = True

    def get_conditions(self) -> DictProxy[Any, Any]:
        self._lazy_init()
        return self._conditions

    def get_waiter_counts(self) -> DictProxy[Any, Any]:
        self._lazy_init()
        return self._waiter_counts

    def get_lock(self) -> MPLock:
        self._lazy_init()
        return self._lock

    def get_shutdown_event(self) -> MPEvent:
        self._lazy_init()
        return self._shutdown_event

    def notify_all_and_clear(self) -> None:
        """Notify all waiting conditions and clear them to prevent new waiters."""
        self._lazy_init()
        with self._lock:
            conditions = list(self._conditions.values())
            # Clear conditions to prevent new waiters
            self._conditions.clear()
            self._waiter_counts.clear()
        for condition in conditions:
            with condition:
                condition.notify_all()


class MultiprocessingSyncManager(AbstractSyncManager):
    """A synchronization manager that uses multiprocessing primitives."""

    _manager: "ServerSyncManager"

    def __init__(self, address: Any = None, authkey: bytes | None = None) -> None:
        if address:
            # Client mode: connect to the server manager.
            manager = ServerSyncManager(address=address, authkey=authkey)
            manager.connect()
            self._manager = manager
            self._is_server = False
        else:
            # Server mode: create the manager that hosts the singletons.
            manager = ServerSyncManager()
            manager.start()
            self._manager = manager
            self._is_server = True
            self._export_connection_details()

        # Get proxies to the singleton objects.
        self._conditions = self._manager.get_conditions()
        self._waiter_counts = self._manager.get_waiter_counts()
        self._lock = self._manager.get_lock()
        self._shutdown_event = self._manager.get_shutdown_event()

    def _export_connection_details(self) -> None:
        """Set environment variables for client processes to connect."""
        if not self._is_server:
            return

        address = self.address
        authkey = current_process().authkey

        if address and authkey:
            os.environ["TRONBYT_MP_MANAGER_ADDR"] = repr(address)
            os.environ["TRONBYT_MP_MANAGER_AUTHKEY"] = base64.b64encode(authkey).decode(
                "ascii"
            )

    @property
    def address(self) -> Any:
        """Get the address of the manager process (server mode only)."""
        if not self._is_server:
            return None
        return self._manager.address

    def is_shutdown(self) -> bool:
        return self._shutdown_event.is_set()

    def release_condition(self, device_id: str) -> None:
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
                # We need to create the Condition object via the manager so it can
                # be shared across processes.
                self._conditions[device_id] = self._manager.Condition()
                self._waiter_counts[device_id] = 0
            self._waiter_counts[device_id] += 1
            condition = self._conditions[device_id]

        return MultiprocessingWaiter(condition, self, device_id)

    def notify(self, device_id: str) -> None:
        """Notify waiters for a given device ID."""
        condition: ConditionType | None = None
        with self._lock:
            if device_id in self._conditions:
                condition = self._conditions[device_id]

        if condition:
            with condition:
                condition.notify_all()

    def _shutdown(self) -> None:
        """Shut down the sync manager."""
        if self._is_server:
            self._shutdown_event.set()
            self._manager.notify_all_and_clear()
            self._manager.shutdown()

    def __enter__(self) -> "MultiprocessingSyncManager":
        return self

    def __exit__(self, exc_type: Any, exc_value: Any, traceback: Any) -> None:
        self._shutdown()


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

    def wait(self, timeout: int) -> bool:
        """Wait for a notification."""
        try:
            message = self._pubsub.get_message(timeout=timeout)
            return message is not None
        except (ValueError, redis.ConnectionError):
            # This can happen if the pubsub connection is closed by another thread
            # while we are waiting for a message.
            return False

    def close(self) -> None:
        """Clean up the waiter."""
        try:
            self._pubsub.unsubscribe(self._device_id)
        except ConnectionError:
            # Ignore connection errors during cleanup
            pass
        finally:
            self._pubsub.close()


class RedisSyncManager(AbstractSyncManager):
    """A synchronization manager that uses Redis Pub/Sub."""

    def __init__(self, redis_url: str) -> None:
        self._redis = redis.from_url(redis_url)  # type: ignore[no-untyped-call]

    def get_waiter(self, device_id: str) -> Waiter:
        """Get a waiter for a given device ID."""
        return RedisWaiter(self._redis, device_id)

    def notify(self, device_id: str) -> None:
        """Notify waiters for a given device ID."""
        self._redis.publish(device_id, NOTIFY_MESSAGE)

    def _shutdown(self) -> None:
        """Shut down the sync manager."""
        self._redis.close()

    def __enter__(self) -> "RedisSyncManager":
        return self

    def __exit__(self, exc_type: Any, exc_value: Any, traceback: Any) -> None:
        self._shutdown()


_sync_manager: AbstractSyncManager | None = None
_sync_manager_lock = Lock()


def get_sync_manager() -> AbstractSyncManager:
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
                    # Check env vars for multiprocessing client mode
                    manager_address_str = os.environ.get("TRONBYT_MP_MANAGER_ADDR")
                    manager_authkey_b64 = os.environ.get("TRONBYT_MP_MANAGER_AUTHKEY")

                    if manager_address_str and manager_authkey_b64:
                        # Client mode for worker processes

                        logger.info("Connecting to parent sync manager...")
                        try:
                            address = ast.literal_eval(manager_address_str)
                            authkey = base64.b64decode(manager_authkey_b64)
                            _sync_manager = MultiprocessingSyncManager(
                                address=address, authkey=authkey
                            )
                        except Exception as e:
                            logger.error(
                                f"Failed to connect to parent sync manager: {e}"
                            )
                            raise
                    else:
                        # Server mode for the main process
                        logger.info(
                            "Using multiprocessing for synchronization (server)"
                        )
                        _sync_manager = MultiprocessingSyncManager()
    assert _sync_manager is not None
    return _sync_manager
