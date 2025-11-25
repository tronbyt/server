# Automatic Database Migration

## 🎉 No Manual Steps Required!

The Tronbyt Server now automatically migrates old databases to the new SQLModel format on startup.

## How It Works

1. **Detection**: On startup, the server checks if you have an old `json_data` table
2. **Migration**: If detected, it automatically runs the Phase 2 migration script
3. **Validation**: Verifies all data was migrated correctly
4. **Backup**: Renames `json_data` to `json_data_backup` (keeps your data safe!)
5. **Startup**: Continues with normal server startup

## What You See

When starting with an old database, you'll see:

```
======================================================================
OLD DATABASE FORMAT DETECTED - Running automatic migration...

Your database uses the old JSON format. Converting to SQLModel.
This is a one-time operation that will:\n  1. Create new relational tables
  2. Migrate all your data
  3. Rename json_data to json_data_backup (keeps your old data safe)
======================================================================

[Migration output...]

✅ Automatic migration completed successfully!
Your data has been migrated to SQLModel format.
```

## Migration Details

The migration:
- **Takes**: Usually 2-10 seconds depending on database size
- **Creates**: New tables: `users`, `devices`, `apps`, `locations`, `recurrence_patterns`
- **Migrates**: All users, devices, apps, locations, and recurrence patterns
- **Validates**: Counts match between old and new tables
- **Preserves**: Old data in `json_data_backup` table

## Rollback (If Needed)

If something goes wrong, you can rollback:

```sql
-- Restore old database (sqlite3)
DROP TABLE users;
DROP TABLE devices;
DROP TABLE apps;
DROP TABLE locations;
DROP TABLE recurrence_patterns;
ALTER TABLE json_data_backup RENAME TO json_data;
```

Or just restore from your database backup.

## Manual Migration (Optional)

You can also run the migration manually:

```bash
# Dry run first to see what will happen
python scripts/migrate_to_sqlmodel.py --dry-run

# Actually run the migration
python scripts/migrate_to_sqlmodel.py
```

## Support

See `MIGRATION_NOTES.md` for complete technical details about all migration phases.
