"""SQLModel for system settings."""

from sqlmodel import Field, SQLModel


class SystemSettingsDB(SQLModel, table=True):
    """SQLModel for system-wide settings (singleton table)."""

    __tablename__ = "system_settings"

    id: int = Field(default=1, primary_key=True)  # Always 1 (singleton)
    system_repo_url: str = ""
