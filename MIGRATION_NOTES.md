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

✅ **Phase 2: COMPLETE - Migration Script Ready**
- Created `scripts/migrate_to_sqlmodel.py`
- Dry-run test successful on production database:
  - 25 users
  - 27 devices
  - 135 apps
  - 17 locations
  - 0 errors
- Includes validation and backup features
- Safe to run on production database

⏳ **Phase 3: Next Steps - Update Code**
- Run actual migration (or test on copy first)
- Update `db.py` functions to use SQLModel queries
- Update routers to use new database models
- Remove old JSON manipulation code

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

## Running the Migration

### Option 1: Test on a Copy First (Recommended)

```bash
# Create a test copy of your database
cp users/usersdb.sqlite users/usersdb-test.sqlite

# Run migration on the test copy
python scripts/migrate_to_sqlmodel.py --db-path users/usersdb-test.sqlite

# If successful, run on production
python scripts/migrate_to_sqlmodel.py
```

### Option 2: Dry-Run First (See What Will Happen)

```bash
# Dry run - shows what would happen, doesn't change anything
python scripts/migrate_to_sqlmodel.py --dry-run

# If looks good, run for real
python scripts/migrate_to_sqlmodel.py
```

### What the Migration Does:

1. ✅ Creates new tables (users, devices, apps, locations, recurrence_patterns)
2. ✅ Reads all users from `json_data` table
3. ✅ Migrates each user with all their devices and apps
4. ✅ Validates counts match (users, devices, apps, locations)
5. ✅ Renames `json_data` → `json_data_backup` (keeps your old data safe!)
6. ✅ New tables are indexed and ready to use

### Safety Features:

- **Dry-run mode** - Test without making changes
- **Automatic backup** - Old table renamed, never deleted
- **Validation** - Counts verified before marking complete
- **Error tracking** - All errors logged with context
- **Transactional** - Database changes are atomic

### After Migration: Phase 3

After successful migration, Phase 3 will update all the `db.py` functions to use SQLModel queries instead of JSON manipulation. This is a larger code change but the data will already be migrated.
