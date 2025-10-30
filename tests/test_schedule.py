import datetime
from zoneinfo import ZoneInfo

from tronbyt_server import db
from tronbyt_server.models.app import App, RecurrencePattern, RecurrenceType, Weekday


def test_get_is_app_schedule_active() -> None:
    assert db.get_is_app_schedule_active_at_time(
        App(
            name="testing",
            iname="testing",
            id="testing",
            path="/testing",
            start_time=datetime.time(18, 0),
            end_time=datetime.time(22, 0),
        ),
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
        App(
            name="testing",
            iname="testing",
            id="testing",
            path="/testing",
            start_time=datetime.time(18, 0),
            end_time=datetime.time(22, 0),
        ),
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
        App(
            name="testing",
            iname="testing",
            id="testing",
            path="/testing",
            start_time=datetime.time(18, 0),
            end_time=datetime.time(22, 0),
        ),
        datetime.datetime(
            year=2025,
            month=1,
            day=1,
            hour=12,
            minute=0,
        ),
    )
    assert db.get_is_app_schedule_active_at_time(
        App(
            name="testing",
            iname="testing",
            id="testing",
            path="/testing",
            start_time=datetime.time(22, 0),
            end_time=datetime.time(6, 0),
        ),
        datetime.datetime(
            year=2025,
            month=1,
            day=1,
            hour=23,
            minute=10,
        ),
    )
    assert not db.get_is_app_schedule_active_at_time(
        App(
            name="testing",
            iname="testing",
            id="testing",
            path="/testing",
            start_time=datetime.time(22, 0),
            end_time=datetime.time(6, 0),
        ),
        datetime.datetime(
            year=2025,
            month=1,
            day=1,
            hour=8,
            minute=10,
        ),
    )
    assert db.get_is_app_schedule_active_at_time(
        App(
            name="testing",
            iname="testing",
            id="testing",
            path="/testing",
            start_time=datetime.time(22, 0),
            end_time=datetime.time(6, 0),
        ),
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
        App(name="testing", iname="testing", id="testing", path="/testing"),
        datetime.datetime(year=2025, month=1, day=1, hour=10, minute=0, second=10),
    )
    assert db.get_is_app_schedule_active_at_time(
        App(
            name="testing",
            iname="testing",
            id="testing",
            path="/testing",
            start_time=datetime.time(18, 0),
            end_time=datetime.time(22, 0),
            days=[Weekday.WEDNESDAY],
        ),
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
        App(
            name="testing",
            iname="testing",
            id="testing",
            path="/testing",
            start_time=datetime.time(18, 0),
            end_time=datetime.time(22, 0),
            days=[Weekday.MONDAY, Weekday.TUESDAY],
        ),
        datetime.datetime(
            year=2025,
            month=1,
            day=1,
            hour=19,
            minute=0,
            second=0,
        ),
    )


def test_daily_recurrence() -> None:
    """Test daily recurrence patterns."""
    # Every day
    app = App(
        name="daily_test",
        iname="daily_test",
        id="daily_test",
        path="/daily_test",
        use_custom_recurrence=True,
        recurrence_type=RecurrenceType.DAILY,
        recurrence_interval=1,
        recurrence_start_date=datetime.date(2025, 1, 1),
    )

    # Should be active every day
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 1, 12, 0)
    )
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 2, 12, 0)
    )
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 15, 12, 0)
    )

    # Every 3 days
    app.recurrence_interval = 3
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 1, 12, 0)
    )  # Start date
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 2, 12, 0)
    )  # +1 day
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 3, 12, 0)
    )  # +2 days
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 4, 12, 0)
    )  # +3 days
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 7, 12, 0)
    )  # +6 days


def test_weekly_recurrence() -> None:
    """Test weekly recurrence patterns."""
    # Every week on Monday and Wednesday
    app = App(
        name="weekly_test",
        iname="weekly_test",
        id="weekly_test",
        path="/weekly_test",
        use_custom_recurrence=True,
        recurrence_type=RecurrenceType.WEEKLY,
        recurrence_interval=1,
        recurrence_start_date=datetime.date(2025, 1, 6),  # Monday, Jan 6, 2025
        recurrence_pattern=RecurrencePattern(
            weekdays=[Weekday.MONDAY, Weekday.WEDNESDAY]
        ),
    )

    # Test first week
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 6, 12, 0)
    )  # Monday
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 7, 12, 0)
    )  # Tuesday
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 8, 12, 0)
    )  # Wednesday
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 9, 12, 0)
    )  # Thursday

    # Test next week
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 13, 12, 0)
    )  # Monday
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 15, 12, 0)
    )  # Wednesday

    # Every 2 weeks (bi-weekly) on Monday
    app.recurrence_interval = 2
    app.recurrence_pattern = RecurrencePattern(weekdays=[Weekday.MONDAY])

    # Week 1: Should be active
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 6, 12, 0)
    )  # Monday week 1

    # Week 2: Should NOT be active (skip week)
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 13, 12, 0)
    )  # Monday week 2

    # Week 3: Should be active again
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 20, 12, 0)
    )  # Monday week 3


def test_monthly_recurrence_day_of_month() -> None:
    """Test monthly recurrence on specific day of month."""
    # 1st of every month
    app = App(
        name="monthly_test",
        iname="monthly_test",
        id="monthly_test",
        path="/monthly_test",
        use_custom_recurrence=True,
        recurrence_type=RecurrenceType.MONTHLY,
        recurrence_interval=1,
        recurrence_start_date=datetime.date(2025, 1, 1),
        recurrence_pattern=RecurrencePattern(day_of_month=1),
    )

    # Should be active on 1st of each month
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 1, 12, 0)
    )
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 2, 1, 12, 0)
    )
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 3, 1, 12, 0)
    )

    # Should NOT be active on other days
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 2, 12, 0)
    )
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 15, 12, 0)
    )

    # 15th of every 2 months
    app.recurrence_interval = 2
    app.recurrence_pattern = RecurrencePattern(day_of_month=15)
    app.recurrence_start_date = datetime.date(2025, 1, 15)

    # Should be active on 15th of Jan, Mar, May, etc.
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 15, 12, 0)
    )  # Month 0
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 2, 15, 12, 0)
    )  # Month 1 (skip)
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 3, 15, 12, 0)
    )  # Month 2


def test_monthly_recurrence_day_of_week() -> None:
    """Test monthly recurrence on specific day of week pattern."""
    # First Monday of every month
    app = App(
        name="monthly_dow_test",
        iname="monthly_dow_test",
        id="monthly_dow_test",
        path="/monthly_dow_test",
        use_custom_recurrence=True,
        recurrence_type=RecurrenceType.MONTHLY,
        recurrence_interval=1,
        recurrence_start_date=datetime.date(2025, 1, 1),
        recurrence_pattern=RecurrencePattern(day_of_week="first_monday"),
    )

    # January 2025: First Monday is Jan 6
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 6, 12, 0)
    )
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 13, 12, 0)
    )  # Second Monday

    # February 2025: First Monday is Feb 3
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 2, 3, 12, 0)
    )

    # Last Friday of every month
    app.recurrence_pattern = RecurrencePattern(day_of_week="last_friday")

    # January 2025: Last Friday is Jan 31
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 31, 12, 0)
    )
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 24, 12, 0)
    )  # Not the last Friday


def test_yearly_recurrence() -> None:
    """Test yearly recurrence patterns."""
    # Every year on the same date
    app = App(
        name="yearly_test",
        iname="yearly_test",
        id="yearly_test",
        path="/yearly_test",
        use_custom_recurrence=True,
        recurrence_type=RecurrenceType.YEARLY,
        recurrence_interval=1,
        recurrence_start_date=datetime.date(2025, 9, 22),  # Today's date
    )

    # Should be active on Sep 22 of each year
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 9, 22, 12, 0)
    )
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2026, 9, 22, 12, 0)
    )
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2027, 9, 22, 12, 0)
    )

    # Should NOT be active on other dates
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 9, 21, 12, 0)
    )
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 9, 23, 12, 0)
    )
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 10, 22, 12, 0)
    )

    # Every 2 years
    app.recurrence_interval = 2

    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 9, 22, 12, 0)
    )  # Year 0
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2026, 9, 22, 12, 0)
    )  # Year 1 (skip)
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2027, 9, 22, 12, 0)
    )  # Year 2


def test_recurrence_with_end_date() -> None:
    """Test recurrence patterns with end dates."""
    app = App(
        name="end_date_test",
        iname="end_date_test",
        id="end_date_test",
        path="/end_date_test",
        use_custom_recurrence=True,
        recurrence_type=RecurrenceType.DAILY,
        recurrence_interval=1,
        recurrence_start_date=datetime.date(2025, 1, 1),
        recurrence_end_date=datetime.date(2025, 1, 5),
    )

    # Should be active within the date range
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 1, 12, 0)
    )
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 3, 12, 0)
    )
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 5, 12, 0)
    )

    # Should NOT be active after end date
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 6, 12, 0)
    )
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 10, 12, 0)
    )


def test_recurrence_with_time_range() -> None:
    """Test recurrence patterns combined with time ranges."""
    app = App(
        name="time_range_test",
        iname="time_range_test",
        id="time_range_test",
        path="/time_range_test",
        use_custom_recurrence=True,
        start_time=datetime.time(9, 0),
        end_time=datetime.time(17, 0),
        recurrence_type=RecurrenceType.WEEKLY,
        recurrence_interval=1,
        recurrence_start_date=datetime.date(2025, 1, 6),  # Monday
        recurrence_pattern=RecurrencePattern(
            weekdays=[Weekday.MONDAY, Weekday.WEDNESDAY, Weekday.FRIDAY]
        ),
    )

    # Should be active on weekdays within time range
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 6, 10, 0)
    )  # Monday 10:00
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 8, 14, 0)
    )  # Wednesday 14:00

    # Should NOT be active on weekdays outside time range
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 6, 8, 0)
    )  # Monday 8:00
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 6, 18, 0)
    )  # Monday 18:00

    # Should NOT be active on non-matching days
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 7, 10, 0)
    )  # Tuesday 10:00


def test_legacy_days_compatibility() -> None:
    """Test that legacy days field still works for backward compatibility."""
    app = App(
        name="legacy_test",
        iname="legacy_test",
        id="legacy_test",
        path="/legacy_test",
        start_time=datetime.time(10, 0),
        end_time=datetime.time(16, 0),
        days=[Weekday.MONDAY, Weekday.WEDNESDAY, Weekday.FRIDAY],
        # No recurrence_type field - should fall back to legacy behavior
    )

    # Should work with legacy days field (Jan 1, 2025 is a Wednesday)
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 1, 12, 0)
    )  # Wednesday
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 2, 12, 0)
    )  # Thursday


def test_edge_cases() -> None:
    """Test edge cases and error conditions."""
    # Test before start date
    app = App(
        name="before_start_test",
        iname="before_start_test",
        id="before_start_test",
        path="/before_start_test",
        use_custom_recurrence=True,
        recurrence_type=RecurrenceType.DAILY,
        recurrence_interval=1,
        recurrence_start_date=datetime.date(2025, 1, 10),
    )

    # Should not be active before start date
    assert not db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 5, 12, 0)
    )
    assert db.get_is_app_schedule_active_at_time(
        app, datetime.datetime(2025, 1, 10, 12, 0)
    )
