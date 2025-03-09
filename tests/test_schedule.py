import datetime
from zoneinfo import ZoneInfo

from tronbyt_server import db


def test_get_is_app_schedule_active():
    assert db.get_is_app_schedule_active_at_time(
        {
            "start_time": "18:00",
            "end_time": "22:00",
        },
        datetime.datetime(
            year=2025,
            month=1,
            day=1,
            hour=19,
            minute=0,
            second=0,
            microsecond=0,
            tzinfo=ZoneInfo("America/New_York"),
        ),
    )
    assert db.get_is_app_schedule_active_at_time(
        {
            "start_time": "18:00",
            "end_time": "22:00",
        },
        datetime.datetime(
            year=2025,
            month=1,
            day=1,
            hour=19,
            minute=0,
            second=0,
        ),
    )
    assert not db.get_is_app_schedule_active_at_time(
        {
            "start_time": "18:00",
            "end_time": "22:00",
        },
        datetime.datetime(
            year=2025,
            month=1,
            day=1,
            hour=12,
            minute=0,
        ),
    )
    assert db.get_is_app_schedule_active_at_time(
        {
            "start_time": "22:00",
            "end_time": "06:00",
        },
        datetime.datetime(
            year=2025,
            month=1,
            day=1,
            hour=23,
            minute=10,
        ),
    )
    assert not db.get_is_app_schedule_active_at_time(
        {
            "start_time": "22:00",
            "end_time": "06:00",
        },
        datetime.datetime(
            year=2025,
            month=1,
            day=1,
            hour=8,
            minute=10,
        ),
    )
    assert db.get_is_app_schedule_active_at_time(
        {
            "start_time": "22:00",
            "end_time": "06:00",
        },
        datetime.datetime(
            year=2025,
            month=1,
            day=1,
            hour=23,
            minute=59,
            second=10,
            microsecond=1000,
        ),
    )
    assert db.get_is_app_schedule_active_at_time(
        {}, datetime.datetime(year=2025, month=1, day=1, hour=10, minute=0, second=10)
    )
    assert db.get_is_app_schedule_active_at_time(
        {
            "start_time": "18:00",
            "end_time": "22:00",
            "days": ["wednesday"],
        },
        datetime.datetime(
            year=2025,
            month=1,
            day=1,
            hour=19,
            minute=0,
            second=0,
        ),
    )
    assert not db.get_is_app_schedule_active_at_time(
        {
            "start_time": "18:00",
            "end_time": "22:00",
            "days": ["monday", "tuesday"],
        },
        datetime.datetime(
            year=2025,
            month=1,
            day=1,
            hour=19,
            minute=0,
            second=0,
        ),
    )
