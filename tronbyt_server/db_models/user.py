"""SQLModel for user."""

from typing import Any, ClassVar, Optional, TYPE_CHECKING
from sqlalchemy import Column, Enum as SAEnum
from sqlmodel import Field, Relationship, SQLModel

from tronbyt_server.models.user import ThemePreference

if TYPE_CHECKING:
    from .device import DeviceDB


class UserDB(SQLModel, table=True):
    """SQLModel for User table."""

    __tablename__: ClassVar[Any] = "users"

    id: Optional[int] = Field(default=None, primary_key=True)
    username: str = Field(unique=True, index=True)
    password: str
    email: str = ""
    api_key: str = Field(default="", index=True)
    theme_preference: ThemePreference = Field(
        default=ThemePreference.SYSTEM,
        sa_column=Column(SAEnum(ThemePreference, native_enum=False, length=10)),
    )
    app_repo_url: str = ""

    # Relationships
    devices: list["DeviceDB"] = Relationship(
        back_populates="user", sa_relationship_kwargs={"cascade": "all, delete-orphan"}
    )
