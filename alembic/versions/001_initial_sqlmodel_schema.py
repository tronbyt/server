"""Initial SQLModel schema

Revision ID: 001
Revises:
Create Date: 2025-11-24

This migration creates the initial SQLModel tables based on the schema
defined in db_models/models.py. This assumes you have already migrated
data from the old json_data table using the Phase 2 migration script.

"""

from typing import Sequence, Union

from alembic import op  # type: ignore[attr-defin
import sqlalchemy as sa

# revision identifiers, used by Alembic.
revision: str = "001"
down_revision: Union[str, None] = None
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    """Create all SQLModel tables."""
    # Create system_settings table
    op.create_table(
        "system_settings",
        sa.Column("id", sa.Integer(), nullable=False),
        sa.Column("system_repo_url", sa.String(), nullable=False),
        sa.PrimaryKeyConstraint("id"),
    )

    # Create users table
    op.create_table(
        "users",
        sa.Column("id", sa.Integer(), nullable=False),
        sa.Column("username", sa.String(), nullable=False),
        sa.Column("password", sa.String(), nullable=False),
        sa.Column("email", sa.String(), nullable=False),
        sa.Column("api_key", sa.String(), nullable=False),
        sa.Column("theme_preference", sa.String(), nullable=False),
        sa.Column("app_repo_url", sa.String(), nullable=False),
        sa.PrimaryKeyConstraint("id"),
    )
    op.create_index(op.f("ix_users_username"), "users", ["username"], unique=True)
    op.create_index(op.f("ix_users_api_key"), "users", ["api_key"], unique=False)

    # Create devices table
    op.create_table(
        "devices",
        sa.Column("id", sa.String(), nullable=False),
        sa.Column("name", sa.String(), nullable=False),
        sa.Column("type", sa.String(), nullable=False),
        sa.Column("api_key", sa.String(), nullable=False),
        sa.Column("img_url", sa.String(), nullable=False),
        sa.Column("ws_url", sa.String(), nullable=False),
        sa.Column("notes", sa.String(), nullable=False),
        sa.Column("brightness", sa.Integer(), nullable=False),
        sa.Column("custom_brightness_scale", sa.String(), nullable=False),
        sa.Column("night_brightness", sa.Integer(), nullable=False),
        sa.Column("dim_brightness", sa.Integer(), nullable=True),
        sa.Column("night_mode_enabled", sa.Boolean(), nullable=False),
        sa.Column("night_mode_app", sa.String(), nullable=False),
        sa.Column("night_start", sa.String(), nullable=True),
        sa.Column("night_end", sa.String(), nullable=True),
        sa.Column("dim_time", sa.String(), nullable=True),
        sa.Column("default_interval", sa.Integer(), nullable=False),
        sa.Column("timezone", sa.String(), nullable=True),
        sa.Column("last_app_index", sa.Integer(), nullable=False),
        sa.Column("pinned_app", sa.String(), nullable=True),
        sa.Column("interstitial_enabled", sa.Boolean(), nullable=False),
        sa.Column("interstitial_app", sa.String(), nullable=True),
        sa.Column("last_seen", sa.DateTime(), nullable=True),
        sa.Column("info", sa.JSON(), nullable=False),
        sa.Column("user_id", sa.Integer(), nullable=False),
        sa.ForeignKeyConstraint(
            ["user_id"],
            ["users.id"],
        ),
        sa.PrimaryKeyConstraint("id"),
    )
    op.create_index(op.f("ix_devices_user_id"), "devices", ["user_id"], unique=False)

    # Create locations table
    op.create_table(
        "locations",
        sa.Column("id", sa.Integer(), nullable=False),
        sa.Column("locality", sa.String(), nullable=False),
        sa.Column("description", sa.String(), nullable=False),
        sa.Column("place_id", sa.String(), nullable=False),
        sa.Column("timezone", sa.String(), nullable=True),
        sa.Column("lat", sa.Float(), nullable=False),
        sa.Column("lng", sa.Float(), nullable=False),
        sa.Column("device_id", sa.String(), nullable=False),
        sa.ForeignKeyConstraint(
            ["device_id"],
            ["devices.id"],
        ),
        sa.PrimaryKeyConstraint("id"),
    )

    # Create apps table
    op.create_table(
        "apps",
        sa.Column("id", sa.Integer(), nullable=False),
        sa.Column("iname", sa.String(), nullable=False),
        sa.Column("name", sa.String(), nullable=False),
        sa.Column("uinterval", sa.Integer(), nullable=False),
        sa.Column("display_time", sa.Integer(), nullable=False),
        sa.Column("notes", sa.String(), nullable=False),
        sa.Column("enabled", sa.Boolean(), nullable=False),
        sa.Column("pushed", sa.Boolean(), nullable=False),
        sa.Column("order", sa.Integer(), nullable=False),
        sa.Column("last_render", sa.Integer(), nullable=False),
        sa.Column("last_render_duration", sa.Integer(), nullable=False),
        sa.Column("path", sa.String(), nullable=True),
        sa.Column("start_time", sa.String(), nullable=True),
        sa.Column("end_time", sa.String(), nullable=True),
        sa.Column("days", sa.JSON(), nullable=False),
        sa.Column("use_custom_recurrence", sa.Boolean(), nullable=False),
        sa.Column("recurrence_type", sa.String(), nullable=False),
        sa.Column("recurrence_interval", sa.Integer(), nullable=False),
        sa.Column("recurrence_start_date", sa.Date(), nullable=True),
        sa.Column("recurrence_end_date", sa.Date(), nullable=True),
        sa.Column("config", sa.JSON(), nullable=False),
        sa.Column("empty_last_render", sa.Boolean(), nullable=False),
        sa.Column("render_messages", sa.JSON(), nullable=False),
        sa.Column("autopin", sa.Boolean(), nullable=False),
        sa.Column("recurrence_pattern", sa.JSON(), nullable=True),
        sa.Column("device_id", sa.String(), nullable=False),
        sa.ForeignKeyConstraint(
            ["device_id"],
            ["devices.id"],
        ),
        sa.PrimaryKeyConstraint("id"),
    )
    op.create_index(op.f("ix_apps_iname"), "apps", ["iname"], unique=False)
    op.create_index(op.f("ix_apps_device_id"), "apps", ["device_id"], unique=False)


def downgrade() -> None:
    """Drop all SQLModel tables."""
    op.drop_table("apps")
    op.drop_table("locations")
    op.drop_table("devices")
    op.drop_table("users")
    op.drop_table("system_settings")
