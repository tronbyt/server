"""FastAPI dependencies."""

import ipaddress
import logging
from datetime import timedelta
from typing import Generator

from fastapi import Depends, Header, HTTPException, Request, status
from fastapi.responses import RedirectResponse, Response
from fastapi_login import LoginManager
from fastapi_login.exceptions import InvalidCredentialsException
from sqlmodel import Session

from tronbyt_server import db
from tronbyt_server.config import Settings, get_settings
from tronbyt_server.db_models import get_session
from tronbyt_server.models import App, Device, User, DeviceID


logger = logging.getLogger(__name__)


class NotAuthenticatedException(Exception):
    """Exception for when a user is not authenticated."""

    pass


manager = LoginManager(
    get_settings().SECRET_KEY,
    "/auth/login",
    use_cookie=True,
    not_authenticated_exception=NotAuthenticatedException,
)


def get_db(
    settings: Settings = Depends(get_settings),
) -> Generator[Session, None, None]:
    """Get a SQLModel database session."""
    session = next(get_session())
    try:
        yield session
    finally:
        session.close()


class UserAndDevice:
    """Container for user and device objects."""

    def __init__(self, user: User, device: Device):
        """Initialize the UserAndDevice object."""
        self.user = user
        self.device = device


class DeviceAndApp:
    """Container for device and app objects."""

    def __init__(self, device: Device, app: App):
        """Initialize the DeviceAndApp object."""
        self.device = device
        self.app = app


def get_device_and_app(
    device_id: DeviceID,
    iname: str,
    user: User = Depends(manager),
) -> DeviceAndApp:
    """Get a device and app from a device ID and app iname."""
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="Device not found"
        )
    app = device.apps.get(iname)
    if not app:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="App not found"
        )
    return DeviceAndApp(device, app)


def get_user_and_device(
    device_id: DeviceID, session: Session = Depends(get_db)
) -> UserAndDevice:
    """Get a user and device from a device ID."""
    user = db.get_user_by_device_id(session, device_id)
    if not user:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="User not found"
        )
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="Device not found"
        )
    return UserAndDevice(user, device)


def check_for_users(
    request: Request, session: Session = Depends(get_db)
) -> None:
    """Check if there are any users in the database."""
    if not db.has_users(session):
        if request.url.path != "/auth/register_owner":
            raise NotAuthenticatedException


def get_user_and_device_from_api_key(
    device_id: str | None = None,
    authorization: str | None = Header(None, alias="Authorization"),
    session: Session = Depends(get_db),
) -> tuple[User | None, Device | None]:
    """Get a user and/or device from an API key."""
    if not authorization:
        raise InvalidCredentialsException

    api_key = (
        authorization.split(" ")[1]
        if authorization.startswith("Bearer ")
        else authorization
    )

    user = db.get_user_by_api_key(session, api_key)
    if user:
        device = user.devices.get(device_id) if device_id else None
        return user, device

    device = db.get_device_by_id(session, device_id) if device_id else None
    if device and device.api_key == api_key:
        user = db.get_user_by_device_id(session, device.id)
        return user, device

    raise InvalidCredentialsException


@manager.user_loader()  # type: ignore
def load_user(username: str) -> User | None:
    """Load a user from the database."""
    session = next(get_db(settings=get_settings()))
    try:
        user = db.get_user(session, username)
        if user:
            return user
        return None
    finally:
        session.close()


def is_trusted_network(client_host: str | None) -> bool:
    """
    Check if the client is from a trusted network (localhost or private networks).

    Trusted networks include:
    - IPv4 localhost: 127.0.0.1
    - IPv6 localhost: ::1
    - IPv4 private networks: 192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12
    - IPv6 unique local addresses (ULA): fc00::/7
    - IPv6 link-local addresses: fe80::/10
    - (deprecated) IPv6 site-local: fec0::/10
    """
    if not client_host:
        return False

    # Check for localhost strings
    if client_host in ("127.0.0.1", "localhost", "::1"):
        return True

    try:
        # Parse the IP address
        ip = ipaddress.ip_address(client_host)

        # Check if it's a private network address (RFC1918)
        # This covers: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
        if ip.is_private:
            return True

        # Also check for loopback explicitly
        if ip.is_loopback:
            return True

    except ValueError:
        # Invalid IP address format
        return False

    return False


def is_auto_login_active(session: Session | None = None) -> bool:
    """
    Check if auto-login is truly active.

    Auto-login is active when:
    - SINGLE_USER_AUTO_LOGIN setting is "1"
    - AND exactly 1 user exists in the system

    Args:
        session: Optional database session. If not provided, creates one.

    Returns:
        True if auto-login is active, False otherwise.
    """
    settings = get_settings()
    if settings.SINGLE_USER_AUTO_LOGIN != "1":
        return False

    # Check user count
    try:
        if session is None:
            session = next(get_session())
            should_close = True
        else:
            should_close = False

        users = db.get_all_users(session)
        result = len(users) == 1

        if should_close:
            session.close()

        return result
    except Exception:
        return False


def auth_exception_handler(
    request: Request, exc: NotAuthenticatedException
) -> Response:
    """
    Redirect the user to the login page if not logged in.

    Special case: If auto-login is active and the request is from a trusted network,
    automatically log in the single user.
    """
    settings = get_settings()

    session = next(get_db(settings=settings))
    try:
        # No users exist - redirect to registration
        if not db.has_users(session):
            return RedirectResponse(request.url_for("get_register_owner"))

        # Check for single-user auto-login mode
        if is_auto_login_active(session):
            # Only from trusted networks (localhost or private networks)
            client_host = request.client.host if request.client else None

            if is_trusted_network(client_host):
                # Get the single user
                users = db.get_all_users(session)
                user = users[0]

                logger.warning(
                    f"Single-user auto-login: Logging in as '{user.username}' "
                    f"from {client_host}"
                )

                # Create access token (30 day expiration for convenience)
                access_token = manager.create_access_token(
                    data={"sub": user.username}, expires=timedelta(days=30)
                )

                # Redirect to home page with cookie set
                response = RedirectResponse(
                    request.url_for("index"), status_code=status.HTTP_302_FOUND
                )
                response.set_cookie(
                    key=manager.cookie_name,
                    value=access_token,
                    max_age=30 * 24 * 60 * 60,  # 30 days
                    httponly=True,
                    samesite="lax",
                )

                return response
    finally:
        session.close()

    # Default: redirect to login
    return RedirectResponse(request.url_for("login"))
