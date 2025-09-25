"""Application configuration."""

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    """Application settings."""

    model_config = SettingsConfigDict(
        env_file=".env", env_file_encoding="utf-8", extra="ignore"
    )

    SECRET_KEY: str = "lksdj;as987q3908475ukjhfgklauy983475iuhdfkjghairutyh"
    MAX_CONTENT_LENGTH: int = 1000 * 1000
    SERVER_HOSTNAME_OR_IP: str = "localhost"
    SERVER_PORT: str = "8000"
    SERVER_PROTOCOL: str = "http"
    USERS_DIR: str = "users"
    DATA_DIR: str = "data"
    PRODUCTION: str = "1"
    DB_FILE: str = "users/usersdb.sqlite"
    LANGUAGES: list[str] = ["en", "de"]
    MAX_USERS: int = 100
    ENABLE_USER_REGISTRATION: str = "0"
    LOG_LEVEL: str = "WARNING"
    SYSTEM_APPS_REPO: str = "https://github.com/tronbyt/apps.git"


settings = Settings()
