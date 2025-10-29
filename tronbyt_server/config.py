"""Application configuration."""

from pydantic_settings import BaseSettings, SettingsConfigDict


from functools import lru_cache


class Settings(BaseSettings):
    """Application settings."""

    model_config = SettingsConfigDict(
        env_file=".env", env_file_encoding="utf-8", extra="ignore"
    )

    SECRET_KEY: str = "lksdj;as987q3908475ukjhfgklauy983475iuhdfkjghairutyh"
    USERS_DIR: str = "users"
    DATA_DIR: str = "data"
    PRODUCTION: str = "1"
    DB_FILE: str = "users/usersdb.sqlite"
    LANGUAGES: list[str] = ["en", "de"]
    MAX_USERS: int = 100
    ENABLE_USER_REGISTRATION: str = "0"
    LOG_LEVEL: str = "WARNING"
    SYSTEM_APPS_REPO: str = "https://github.com/tronbyt/apps.git"
    REDIS_URL: str | None = None


@lru_cache
def get_settings() -> Settings:
    """Return the settings object."""
    return Settings()
