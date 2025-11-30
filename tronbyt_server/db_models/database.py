"""Database configuration for SQLModel."""

from typing import Generator
from sqlmodel import Session, SQLModel, create_engine

from tronbyt_server.config import get_settings


# Create engine - will use SQLite with the same DB file
def get_engine() -> create_engine:
    """Get the database engine."""
    db_url = f"sqlite:///{get_settings().DB_FILE}"
    return create_engine(
        db_url,
        echo=False,  # Set to True for SQL debugging
        connect_args={"check_same_thread": False, "timeout": 10},
    )


engine = get_engine()


def create_db_and_tables() -> None:
    """Create all tables in the database."""
    # Use the global engine from this module to support test overrides
    from tronbyt_server import db_models

    SQLModel.metadata.create_all(db_models.engine)


def get_session() -> Generator[Session, None, None]:
    """Get a database session for dependency injection."""
    with Session(engine) as session:
        yield session
