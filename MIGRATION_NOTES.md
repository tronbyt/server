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

✅ **Phase 3: COMPLETE - Code Updated to Use SQLModel**

### What We Did:

#### 1. Created Comprehensive CRUD Operations (`tronbyt_server/db_models/operations.py`)
- **User operations**: `get_user_by_username`, `get_user_by_api_key`, `get_all_users_db`, `has_users`, `create_user`, `update_user`, `delete_user`
- **Device operations**: `get_device_by_id`, `get_devices_by_user_id`, `get_user_by_device_id`, `create_device`, `update_device`, `delete_device`
- **App operations**: `get_app_by_id`, `get_app_by_device_and_iname`, `get_apps_by_device`, `create_app`, `update_app`, `delete_app`
- **Location operations**: `get_location_by_device`, `create_location`, `update_location`, `delete_location`
- **Recurrence pattern operations**: Complete CRUD for app recurrence patterns
- **Conversion helpers**: `load_user_full`, `load_device_full`, `load_app_full` - Load complete objects with all relationships
- **Save helpers**: `save_user_full`, `save_device_full`, `save_app_full` - Save complete objects with all nested data

#### 2. Updated Dependencies (`tronbyt_server/dependencies.py`)
- Changed `get_db()` to return `Session` instead of `sqlite3.Connection`
- Updated all dependency functions to use `Session`:
  - `get_user_and_device`
  - `check_for_users`
  - `get_user_and_device_from_api_key`
  - `load_user`
  - `is_auto_login_active`
  - `auth_exception_handler`

#### 3. Refactored Database Functions (`tronbyt_server/db.py`)
- **Updated all user functions**:
  - `get_user(session, username)` - Now uses SQLModel queries
  - `auth_user(session, username, password)` - Updated to use Session
  - `save_user(session, user, new_user)` - Saves complete user with all devices and apps
  - `delete_user(session, username)` - Deletes user and cascades to devices/apps
  - `get_all_users(session)` - Loads all users with full relationship data
  - `has_users(session)` - Quick check for user existence
  - `get_user_by_api_key(session, api_key)` - API key lookup

- **Updated all device functions**:
  - `get_device_by_id(session, device_id)` - Loads device with apps and location
  - `get_user_by_device_id(session, device_id)` - Finds user by device

- **Updated all app functions**:
  - `save_app(session, device_id, app)` - Saves app with recurrence pattern
  - `save_render_messages(session, user, device, app, messages)` - Updates render messages
  - `add_pushed_app(session, device_id, installation_id)` - Handles pushed apps

- **Updated initialization**:
  - `init_db()` - Now creates SQLModel tables instead of JSON tables
  - Removed old JSON migration code

#### 4. Updated Routers
- **auth.py**: Changed all `sqlite3.Connection` type hints to `Session`
- All router endpoints now use the new SQLModel-based functions

#### 5. Updated Startup
- `startup.py`: Updated to call `init_db()` without parameters

### Key Changes:

1. **No More JSON Manipulation**: All database operations now use proper SQL queries through SQLModel
2. **Proper Relationships**: Users → Devices → Apps → Recurrence Patterns are properly linked with foreign keys
3. **Type Safety**: SQLModel provides full type checking for database operations
4. **Better Performance**: Direct SQL queries are faster than JSON manipulation
5. **Data Integrity**: Foreign key constraints ensure referential integrity
6. **Cleaner Code**: Separation of concerns with operations in `db_models/operations.py`

### Migration Path:

Users who ran Phase 2 migration will automatically benefit from Phase 3 changes:
1. Phase 2 migrated data from `json_data` table to proper SQL tables
2. Phase 3 updated the code to use those SQL tables
3. No additional data migration needed - just code changes

---

✅ **Phase 4: COMPLETE - Alembic Integration for Future Migrations**

### What We Did:

#### 1. Added Alembic Dependency
- Added `alembic>=1.14.0` to `pyproject.toml`
- Alembic is the standard migration tool for SQLAlchemy/SQLModel projects

#### 2. Initialized Alembic Configuration
- Created `alembic.ini` - Main Alembic configuration file
- Created `alembic/env.py` - Environment configuration that integrates with SQLModel
- Created `alembic/script.py.mako` - Template for generating migration files
- Created `alembic/versions/` - Directory for migration files

#### 3. Created Initial Migration
- `alembic/versions/001_initial_sqlmodel_schema.py` - Defines the current schema
- This migration creates all tables: users, devices, apps, locations, recurrence_patterns
- Includes both upgrade (create tables) and downgrade (drop tables) operations

#### 4. Integrated Alembic into Startup
- Updated `init_db()` to automatically run Alembic migrations on startup
- Migrations are applied to "head" (latest version) automatically
- Gracefully handles case where Alembic is not installed

### How to Use Alembic:

#### Creating New Migrations

When you modify models in `tronbyt_server/db_models/models.py`:

```bash
# Auto-generate a migration (recommended)
alembic revision --autogenerate -m "add user preferences table"

# Or create an empty migration file to edit manually
alembic revision -m "custom migration"
```

#### Running Migrations Manually

```bash
# Upgrade to latest version
alembic upgrade head

# Upgrade to specific version
alembic upgrade <revision_id>

# Downgrade one version
alembic downgrade -1

# Show current version
alembic current

# Show migration history
alembic history --verbose
```

#### Migration Files

- Migration files are stored in `alembic/versions/`
- Each migration has `upgrade()` and `downgrade()` functions
- Migrations are automatically applied on application startup via `init_db()`

### Benefits of Alembic:

✅ **Version Control for Schema** - Track database schema changes over time
✅ **Reproducible Deployments** - Apply same migrations across environments
✅ **Rollback Capability** - Downgrade to previous schema versions
✅ **Auto-generation** - Alembic can detect model changes and generate migrations
✅ **Team Collaboration** - Share schema changes through version control

### Future Schema Changes:

When you need to modify the database schema:

1. **Modify the models** in `tronbyt_server/db_models/models.py`
2. **Generate migration**: `alembic revision --autogenerate -m "description"`
3. **Review the migration** file in `alembic/versions/`
4. **Test the migration**: `alembic upgrade head`
5. **Commit** both the model changes and migration file

The migration will be automatically applied on next server startup.

### SQLite Considerations:

- Alembic is configured with `render_as_batch=True` for SQLite compatibility
- SQLite has limited ALTER TABLE support, so some changes require table recreation
- Alembic handles this automatically in batch mode

---

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
