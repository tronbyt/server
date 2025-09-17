"""
Main application file for the Tronbyt server.

This file initializes the FastAPI application, includes the routers,
and sets up startup events.
"""
import logging
from fastapi import FastAPI
from tronbyt_server.api import router as api_router
from tronbyt_server.auth import router as auth_router
from tronbyt_server.manager import router as manager_router
from tronbyt_server.db import init_db
from tronbyt_server.pixlet_utils import initialize_pixlet_library
from tronbyt_server.config import get_settings

app = FastAPI()

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

@app.on_event("startup")
def on_startup():
    init_db()
    initialize_pixlet_library()

app.include_router(api_router, prefix="/api/v1")
app.include_router(auth_router)
app.include_router(manager_router)
