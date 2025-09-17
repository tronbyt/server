from functools import lru_cache
from typing import List

from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    secret_key: str = "lksdj;as987q3908475ukjhfgklauy983475iuhdfkjghairutyh"
    max_content_length: int = 1000 * 1000  # 1mbyte upload size limit
    server_hostname: str = "localhost"
    server_protocol: str = "http"
    main_port: str = "8000"
    users_dir: str = "users"
    data_dir: str = "data"
    production: str = "1"
    db_file: str = "users/usersdb.sqlite"
    languages: List[str] = ["en", "de"]
    max_users: int = 100
    enable_user_registration: str = "0"
    testing: bool = False
    session_cookie_secure: bool = False
    session_cookie_samesite: str = "Lax"
    log_level: str = "WARNING"
    redis_url: str = "redis://localhost"

    class Config:
        env_file = ".env"


@lru_cache()
def get_settings():
    return Settings()
