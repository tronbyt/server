# SQLModel Migration - Phase 1 Complete

## What We've Done

### 1. Added SQLModel Dependency
- Added `sqlmodel>=0.0.25` to `pyproject.toml`
- Installed in the venv_mypy environment

### 2. Created New Database Models
Location: `tronbyt_server/db_models/`

#### Files Created:
- `database.py` - Database engine and session configuration
- `models.py` - SQLModel table definitions
- `__init__.py` - Package exports

#### Tables Defined:
1. **users** - User accounts
   - Primary key: `id` (auto-increment integer)
   - Unique index on `username`
   - Index on `api_key`

2. **devices** - User devices
   - Primary key: `id` (8-char hex string from original)
   - Foreign key to `users.id`
   - Relationships: belongs to user, has many apps, has one location

3. **apps** - Device applications
   - Primary key: `id` (auto-increment integer)
   - Foreign key to `devices.id`
   - Relationship: belongs to device, has one recurrence_pattern

4. **locations** - Device locations
   - Primary key: `id` (auto-increment integer)
   - Foreign key to `devices.id`
   - Relationship: belongs to device

5. **recurrence_patterns** - App scheduling patterns
   - Primary key: `id` (auto-increment integer)
   - Foreign key to `apps.id`
   - Relationship: belongs to app

### 3. Key Design Decisions

#### Storage Simplifications:
- **Brightness**: Stored as integers 0-100 (convert to/from `Brightness` objects in code)
- **Enums**: Stored as strings (DeviceType, ThemePreference, RecurrenceType)
- **Times**: Stored as HH:MM strings (start_time, end_time, etc.)
- **Dates**: Native date types for recurrence dates
- **JSON Columns**:
  - `device.info` - device information dict
  - `app.config` - app configuration dict
  - `app.days` - list of weekday strings
  - `app.render_messages` - list of strings
  - `recurrence_pattern.weekdays` - list of weekday strings

#### Relationships:
- User → Devices (one-to-many)
- Device → Apps (one-to-many)
- Device → Location (one-to-one, optional)
- App → RecurrencePattern (one-to-one, optional)

## Current Status

✅ **Phase 1: COMPLETE**
- SQLModel installed
- Models defined
- Database configuration created
- Tables can be created successfully

⏳ **Phase 2: Next Steps - Migration Script**
- Write script to read from `json_data` table
- Create new users/devices/apps records
- Validate all data migrated correctly
- Keep old table as backup

## Testing Phase 1

```python
# Test that models import
from tronbyt_server.db_models import UserDB, DeviceDB, AppDB

# Test that tables can be created
from tronbyt_server.db_models import create_db_and_tables
create_db_and_tables()
```

## File Structure

```
tronbyt_server/
├── models/           # Original Pydantic models (keep for now)
│   ├── user.py
│   ├── device.py
│   └── app.py
└── db_models/        # New SQLModel models
    ├── __init__.py
    ├── database.py   # Engine, session config
    └── models.py     # Table definitions
```

## Next: Phase 2

Create `scripts/migrate_to_sqlmodel.py` that will:
1. Connect to existing database
2. Read all records from `json_data` table
3. Parse JSON → create SQLModel instances
4. Save to new tables
5. Validate counts and data integrity
6. Rename `json_data` → `json_data_backup`

After successful migration, Phase 3 will update all the `db.py` functions to use SQLModel queries instead of JSON manipulation.
